package relay

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/serial"
)

type stubCompleter struct {
	resp llm.Message
	err  error
}

func (s stubCompleter) Complete(ctx context.Context, messages []llm.Message, tools []llm.Tool) (llm.Message, error) {
	return s.resp, s.err
}

func TestCallAndDispatchSilentCompletionIsIdleWithoutHistoryMutation(t *testing.T) {
	r := &Relay{
		LLM:          stubCompleter{resp: llm.Message{}},
		History:      NewHistory(),
		SystemPrompt: "soul",
	}
	r.History.Append("u", llm.Message{Role: "user", Content: "Hi"})

	idle, err := r.callAndDispatch(context.Background(), "u")
	if err != nil {
		t.Fatalf("callAndDispatch error = %v", err)
	}
	if !idle {
		t.Fatalf("idle = false, want true")
	}
	if got := r.History.Get("u"); len(got) != 1 {
		t.Fatalf("history len = %d, want 1", len(got))
	}
	if len(r.textOutQueue) != 0 {
		t.Fatalf("textOutQueue len = %d, want 0", len(r.textOutQueue))
	}
}

func TestCompletionGraceWindow(t *testing.T) {
	r := &Relay{}
	if got := r.completionGraceWindow(false, false); got != 0 {
		t.Fatalf("grace window enabled without completion")
	}
	if got := r.completionGraceWindow(false, true); got != 250*time.Millisecond {
		t.Fatalf("grace window disabled after silent completion")
	}

	r.textOutQueue = []byte("x")
	if got := r.completionGraceWindow(false, true); got != 0 {
		t.Fatalf("grace window enabled while text is queued")
	}

	r.textOutQueue = nil
	r.waitingTool = true
	if got := r.completionGraceWindow(true, false); got != 0 {
		t.Fatalf("grace window enabled while tool is in flight")
	}

	r.waitingTool = false
	r.basicRunning = true
	if got := r.completionGraceWindow(true, false); got != 0 {
		t.Fatalf("grace window enabled while BASIC is still running")
	}
}

func TestAppendC64LLMEventUsesBackendCompatibleUserRole(t *testing.T) {
	r := &Relay{History: NewHistory()}

	r.appendC64LLMEvent("u", "[heartbeat] idle for 10 minutes")

	got := r.History.Get("u")
	if len(got) != 1 {
		t.Fatalf("history len = %d, want 1", len(got))
	}
	if got[0].Role != "user" {
		t.Fatalf("role = %q, want user", got[0].Role)
	}
	if got[0].Content != "[heartbeat] idle for 10 minutes" {
		t.Fatalf("content = %q", got[0].Content)
	}
}

func TestAcquireMessageGateWaitsUntilRelease(t *testing.T) {
	r := &Relay{
		msgRetryBase: 2 * time.Millisecond,
		msgRetryMax:  4 * time.Millisecond,
	}
	if err := r.acquireMessageGate(context.Background()); err != nil {
		t.Fatalf("first acquire error = %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		acquired <- r.acquireMessageGate(context.Background())
	}()

	select {
	case err := <-acquired:
		t.Fatalf("second acquire returned too early: %v", err)
	case <-time.After(5 * time.Millisecond):
	}

	r.releaseMessageGate()

	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second acquire error = %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("second acquire did not complete after release")
	}

	r.releaseMessageGate()
}

func TestAcquireMessageGateHonorsContextWhileBusy(t *testing.T) {
	r := &Relay{
		msgRetryBase: 5 * time.Millisecond,
		msgRetryMax:  5 * time.Millisecond,
	}
	if err := r.acquireMessageGate(context.Background()); err != nil {
		t.Fatalf("first acquire error = %v", err)
	}
	defer r.releaseMessageGate()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Millisecond)
	defer cancel()

	err := r.acquireMessageGate(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire error = %v, want deadline exceeded", err)
	}
}

func TestAcquireMessageGateAllowsOnlyOneConcurrentHolder(t *testing.T) {
	r := &Relay{
		msgRetryBase: time.Millisecond,
		msgRetryMax:  2 * time.Millisecond,
	}

	if err := r.acquireMessageGate(context.Background()); err != nil {
		t.Fatalf("initial acquire error = %v", err)
	}

	var maxHolders atomic.Int32
	var currentHolders atomic.Int32
	releaseNext := make(chan struct{}, 2)
	errCh := make(chan error, 2)

	startWaiter := func() {
		go func() {
			if err := r.acquireMessageGate(context.Background()); err != nil {
				errCh <- err
				return
			}

			holders := currentHolders.Add(1)
			for {
				old := maxHolders.Load()
				if holders <= old || maxHolders.CompareAndSwap(old, holders) {
					break
				}
			}

			<-releaseNext
			currentHolders.Add(-1)
			r.releaseMessageGate()
			errCh <- nil
		}()
	}

	startWaiter()
	startWaiter()

	time.Sleep(5 * time.Millisecond)
	r.releaseMessageGate()

	releaseNext <- struct{}{}
	releaseNext <- struct{}{}

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("waiter error = %v", err)
		}
	}

	if got := maxHolders.Load(); got != 1 {
		t.Fatalf("max concurrent holders = %d, want 1", got)
	}
}

func TestAcquireMessageGateReleasesWaitersInFIFOOrder(t *testing.T) {
	r := &Relay{}
	if err := r.acquireMessageGate(context.Background()); err != nil {
		t.Fatalf("initial acquire error = %v", err)
	}

	orderCh := make(chan int, 2)
	errCh := make(chan error, 2)
	releaseCh := make(chan struct{}, 2)
	readyCh := make(chan struct{}, 2)

	startWaiter := func(n int) {
		go func() {
			readyCh <- struct{}{}
			if err := r.acquireMessageGate(context.Background()); err != nil {
				errCh <- err
				return
			}
			orderCh <- n
			<-releaseCh
			r.releaseMessageGate()
			errCh <- nil
		}()
	}

	startWaiter(1)
	<-readyCh
	startWaiter(2)
	<-readyCh

	time.Sleep(5 * time.Millisecond)
	r.releaseMessageGate()

	first := <-orderCh
	if first != 1 {
		t.Fatalf("first waiter = %d, want 1", first)
	}
	releaseCh <- struct{}{}

	second := <-orderCh
	if second != 2 {
		t.Fatalf("second waiter = %d, want 2", second)
	}
	releaseCh <- struct{}{}

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("waiter error = %v", err)
		}
	}
}

func TestTryAcquireMessageGateReturnsFalseWhileBusy(t *testing.T) {
	r := &Relay{}
	if !r.tryAcquireMessageGate() {
		t.Fatal("first tryAcquireMessageGate returned false")
	}
	if r.tryAcquireMessageGate() {
		t.Fatal("second tryAcquireMessageGate returned true while busy")
	}
	r.releaseMessageGate()
}

func TestCanSendOverlappingMessageRequiresSteadyStateActivity(t *testing.T) {
	r := &Relay{}
	if r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = true without system prompt")
	}

	r.SystemPrompt = "ready"
	if r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = true without active turn state")
	}

	r.basicRunning = true
	if !r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = false while BASIC is running")
	}

	r.basicRunning = false
	r.completionDrain = true
	if !r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = false during completion drain")
	}

	r.completionDrain = false
	r.textOutQueue = []byte("queued")
	if r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = true with only bridge text queue activity")
	}

	r.textOutQueue = nil
	r.textInFlight = []byte("chunk")
	if r.canSendOverlappingMessage() {
		t.Fatal("canSendOverlappingMessage = true with only bridge text in-flight activity")
	}
}

func TestCanStartRunningOverlapAllowsOnlySingleQueuedRunningSender(t *testing.T) {
	r := &Relay{}
	if r.canStartRunningOverlap() {
		t.Fatal("canStartRunningOverlap = true without running state")
	}

	r.basicRunning = true
	r.msgGateBusy = true
	if !r.canStartRunningOverlap() {
		t.Fatal("canStartRunningOverlap = false for first running overlap sender")
	}

	r.msgGateWaiters = append(r.msgGateWaiters, make(chan struct{}))
	if r.canStartRunningOverlap() {
		t.Fatal("canStartRunningOverlap = true with queued running waiters")
	}

	r.msgGateWaiters = nil
	r.waitingTool = true
	if r.canStartRunningOverlap() {
		t.Fatal("canStartRunningOverlap = true while tool wait is active")
	}

	r.waitingTool = false
	r.basicRunning = false
	r.completionDrain = true
	if r.canStartRunningOverlap() {
		t.Fatal("canStartRunningOverlap = true during completion drain")
	}
}

func TestDispatchAckWaiterDeliversMatchingAck(t *testing.T) {
	r := &Relay{}
	waiter := r.registerAckWaiter(7)
	defer r.unregisterAckWaiter(7)

	f := serial.Frame{Type: serial.FrameAck, Payload: []byte{7}}
	if !r.dispatchAckWaiter(f) {
		t.Fatal("dispatchAckWaiter returned false")
	}

	select {
	case got := <-waiter:
		if got.Type != serial.FrameAck || string(got.Payload) != string(f.Payload) {
			t.Fatalf("got %#v, want %#v", got, f)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("waiter did not receive ACK")
	}
}

func TestDispatchAckWaiterIgnoresUnknownAckID(t *testing.T) {
	r := &Relay{}

	f := serial.Frame{Type: serial.FrameAck, Payload: []byte{9}}
	if r.dispatchAckWaiter(f) {
		t.Fatal("dispatchAckWaiter returned true for unknown ACK id")
	}
}

func TestWaitForAckWaiterAcceptsMatchingAck(t *testing.T) {
	r := &Relay{}
	waiter := make(chan serial.Frame, 1)
	waiter <- serial.Frame{Type: serial.FrameAck, Payload: []byte{3}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := r.waitForAckWaiter(ctx, waiter, 3); err != nil {
		t.Fatalf("waitForAckWaiter error = %v", err)
	}
}

func TestCompletionGraceWindowExtendsForQueuedWaiters(t *testing.T) {
	r := &Relay{}

	if got := r.completionGraceWindow(true, false); got != 250*time.Millisecond {
		t.Fatalf("completionGraceWindow = %v, want %v", got, 250*time.Millisecond)
	}

	r.msgGateBusy = true
	r.msgGateWaiters = append(r.msgGateWaiters, make(chan struct{}))
	if got := r.completionGraceWindow(true, false); got != time.Second {
		t.Fatalf("completionGraceWindow with queued waiters = %v, want %v", got, time.Second)
	}

	r.waitingTool = true
	if got := r.completionGraceWindow(true, false); got != 0 {
		t.Fatalf("completionGraceWindow while waitingTool = %v, want 0", got)
	}
}
