package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/coder/aicommit"
	"github.com/coder/pretty"
	"github.com/coder/serpent"
	"github.com/muesli/termenv"
	"github.com/sashabaranov/go-openai"
)

var colorProfile = termenv.ColorProfile()

func errorf(format string, args ...any) {
	c := pretty.FgColor(colorProfile.Color("#ff0000"))
	pretty.Fprintf(os.Stderr, c, format, args...)
}

func run(inv *serpent.Invocation, client *openai.Client) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}

	msgs, err := aicommit.BuildPrompt(inv.Stdout, workdir, 64000)
	if err != nil {
		return err
	}

	ctx := inv.Context()

	stream, err := client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    openai.GPT4o,
		Stream:   true,
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
				return nil
			}
			return err
		}
		c := resp.Choices[0].Delta.Content
		fmt.Fprintf(inv.Stdout, "%s", c)
	}
}

func main() {
	var openAIKey string
	cmd := &serpent.Command{
		Use:   "aicommit",
		Short: "aicommit is a tool for generating commit messages",
		Handler: func(inv *serpent.Invocation) error {
			client := openai.NewClient(openAIKey)
			return run(inv, client)
		},
		Options: []serpent.Option{
			{
				Name:        "openai-key",
				Description: "The OpenAI API key to use.",
				Env:         "OPENAI_API_KEY",
				Value:       serpent.StringOf(&openAIKey),
				Required:    true,
			},
		},
	}

	err := cmd.Invoke().WithOS().Run()
	if err != nil {
		var unknownCmdErr *serpent.UnknownSubcommandError
		if errors.As(err, &unknownCmdErr) {
			// Unknown command is printed by the help function for some reason.
			os.Exit(1)
		}
		var runCommandErr *serpent.RunCommandError
		if errors.As(err, &runCommandErr) {
			errorf("%s\n", runCommandErr.Err)
			os.Exit(1)
		}

		errorf("error: %s\n", err)
		os.Exit(1)
	}
}
