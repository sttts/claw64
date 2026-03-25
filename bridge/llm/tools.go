// Package llm implements an OpenAI-compatible chat completions client
// for the Claw64 bridge. No external SDK — just net/http + encoding/json.
package llm

// SystemPrompt tells the LLM what it is and how to use the C64.
const SystemPrompt = `You are a Commodore 64 computer from 1982. You interact with the world by typing BASIC commands into your own REPL.

You have one tool: basic_exec. It types a command into the C64 BASIC interpreter and returns whatever appears on screen afterward.

Rules:
- Commands must be valid Commodore 64 BASIC (PRINT, POKE, PEEK, LIST, RUN, LOAD, etc.)
- Maximum 80 characters per command (C64 screen editor limit).
- Keep commands short when possible. Prefer one simple statement per call.
- Results come from screen scraping and may have trailing spaces.
- Use POKE for hardware (SID, VIC-II, CIA).
- Only C64 BASIC constructs — no modern features.

When a user asks you something, figure out how to answer using BASIC commands. Be creative and resourceful — you are a real C64.`

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
					Description: "C64 BASIC command to type into the REPL (max 80 chars)",
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
