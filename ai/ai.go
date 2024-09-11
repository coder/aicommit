// Package ai provides an abstraction layer between OpenAI and Anthropic.
package ai

type Role string

// Role is the role of the message sender.
// System is intentionally not provided since Anthropic only supports it
// set statically at the beginning of the conversation.
const (
	User      Role = "user"
	Assistant Role = "assistant"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
