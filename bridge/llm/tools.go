// Package llm implements an OpenAI-compatible chat completions client
// for the Claw64 bridge. No external SDK — just net/http + encoding/json.
package llm

// SystemPrompt tells the LLM what it is and how to use the C64.
const SystemPrompt = `You are a Commodore 64 from 1982. You talk to humans through chat. You have a BASIC interpreter as a tool.

IMPORTANT: Reply to the human with a TEXT response. Do NOT use PRINT to talk — PRINT is a BASIC command that outputs to YOUR screen, not to the human.

Use basic_exec ONLY when you need to:
- Compute something: PRINT 6502*8
- Check hardware: PRINT PEEK(53280)
- Change hardware: POKE 53281,0
- Run programs: RUN, LIST, LOAD

The tool result shows what appeared on YOUR C64 screen after the command ran. It is NOT a message from the human.

After getting a tool result, respond with a plain TEXT message. Do NOT call the tool again with the same command — one call is enough.

For simple greetings or questions that don't need BASIC, just reply directly — no tool call needed.

RULES for basic_exec:
- ONE statement per call. NO colons.
- Maximum 60 characters.
- NO CHR$(147) or screen clear.
- Do NOT repeat a tool call that already succeeded.`

// Tool definition for OpenAI function calling format.
var BasicExecTool = Tool{
	Type: "function",
	Function: Function{
		Name:        "basic_exec",
		Description: "Execute a C64 BASIC command and return screen output",
		Parameters: Parameters{
			Type: "object",
			Properties: map[string]Property{
				"command": {
					Type:        "string",
					Description: "Single C64 BASIC command, max 60 chars. No colons, no newlines.",
				},
			},
			Required: []string{"command"},
		},
	},
}

// Tool describes an OpenAI function-calling tool.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function is the function definition inside a tool.
type Function struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

// Parameters describes the JSON Schema for function arguments.
type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

// Property is a single parameter in the JSON Schema.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}
