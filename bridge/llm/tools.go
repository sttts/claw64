package llm

// Tool definition for OpenAI function calling format.
var BasicExecTool = Tool{
	Type: "function",
	Function: Function{
		Name:        "exec",
		Description: "Send one C64 BASIC input line",
		Parameters: Parameters{
			Type: "object",
			Properties: map[string]Property{
				"command": {
					Type:        "string",
					Description: "Single-line C64 BASIC input payload",
				},
			},
			Required: []string{"command"},
		},
	},
}

// TextScreenshotTool asks the C64 for the current visible text screen.
var TextScreenshotTool = Tool{
	Type: "function",
	Function: Function{
		Name:        "screen",
		Description: "Request the current visible C64 text screen",
		Parameters: Parameters{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	},
}

// BasicStopTool requests a RUN/STOP-style break on the C64.
var BasicStopTool = Tool{
	Type: "function",
	Function: Function{
		Name:        "stop",
		Description: "Request RUN/STOP for the current C64 BASIC program",
		Parameters: Parameters{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	},
}

// BasicStatusTool asks whether BASIC is still running or sitting at READY.
var BasicStatusTool = Tool{
	Type: "function",
	Function: Function{
		Name:        "status",
		Description: "Request current C64 BASIC execution status",
		Parameters: Parameters{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
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
