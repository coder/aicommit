package main

import (
	"io"
	"os"

	"github.com/coder/aicommit"
	"github.com/coder/serpent"
	"github.com/sashabaranov/go-openai"
)

func lint(inv *serpent.Invocation, opts runOptions) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Build linting prompt considering role, style guide, commit message
	msgs, err := aicommit.BuildLintPrompt(inv.Stdout, workdir, opts.lint)
	if err != nil {
		return err
	}
	if len(opts.context) > 0 {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem,
			Content: "The user has provided additional context that MUST be" +
				" included in the commit message",
		})
		for _, context := range opts.context {
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: context,
			})
		}
	}

	ctx := inv.Context()
	if debugMode {
		for _, msg := range msgs {
			debugf("%s (%v tokens)\n%s\n", msg.Role, aicommit.CountTokens(msg), msg.Content)
		}
	}

	// Stream AI response
	stream, err := opts.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:       opts.model,
		Stream:      true,
		Temperature: 0,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
		Messages: msgs,
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				debugf("stream EOF")
				break
			}
			return err
		}

		if len(resp.Choices) > 0 {
			inv.Stdout.Write([]byte(resp.Choices[0].Delta.Content))
		} else {
			inv.Stdout.Write([]byte("\n"))
		}

		// Usage is only sent in the last message.
		if resp.Usage != nil {
			debugf("total tokens: %d", resp.Usage.TotalTokens)
		}
	}
	return nil
}
