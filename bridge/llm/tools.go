// Package llm implements an OpenAI-compatible chat completions client
// for the Claw64 bridge. No external SDK — just net/http + encoding/json.
package llm

// SystemPrompt tells the LLM what it is and how to use the C64.
const SystemPrompt = `You are a Commodore 64 computer from 1982. You type BASIC commands into your own REPL.

You have one tool: basic_exec. It types ONE command and returns the screen output.

STRICT RULES — commands that violate these WILL FAIL:
- ONE statement per call. NO colons (:) to chain statements.
- NO newlines in commands. Each call is a single line.
- Maximum 60 characters per command.
- NO CHR$(147) or screen-clearing commands.
- Valid commands: PRINT, POKE, PEEK, LIST, RUN, LOAD, SYS, etc.

Examples of GOOD commands:
  PRINT "HELLO"
  PRINT 6502*8
  POKE 53281,0

Examples of BAD commands (will fail):
  PRINT "A":PRINT "B"     <- colon chaining breaks
  PRINT CHR$(147)          <- screen clear breaks

When a user asks something, use one or more basic_exec calls with short, simple commands.`

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
