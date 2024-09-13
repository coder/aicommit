package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/coder/aicommit"
	"github.com/coder/pretty"
	"github.com/coder/serpent"
	"github.com/muesli/termenv"
	"github.com/sashabaranov/go-openai"
)

var colorProfile = termenv.ColorProfile()

func errorf(format string, args ...any) {
	c := pretty.FgColor(colorProfile.Color("#ff0000"))
	pretty.Fprintf(os.Stderr, c, "err: "+format, args...)
}

var debugMode = os.Getenv("AICOMMIT_DEBUG") != ""

func debugf(format string, args ...any) {
	if !debugMode {
		return
	}
	// Gray
	c := pretty.FgColor(colorProfile.Color("#808080"))
	pretty.Fprintf(os.Stderr, c, "debug: "+format+"\n", args...)
}

func getLastCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func resolveRef(ref string) (string, error) {
	cmd := exec.Command("git", "rev-parse", ref)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func formatShellCommand(cmd *exec.Cmd) string {
	buf := &strings.Builder{}
	buf.WriteString(filepath.Base(cmd.Path))
	for _, arg := range cmd.Args[1:] {
		buf.WriteString(" ")
		buf.WriteString(shellescape.Quote(arg))
	}
	return buf.String()
}

func cleanAIMessage(msg string) string {
	// As reported by Ben, sometimes GPT returns the message
	// wrapped in a code block.
	if strings.HasPrefix(msg, "```") {
		msg = strings.TrimSuffix(msg, "```")
		msg = strings.TrimPrefix(msg, "```")
	}
	msg = strings.TrimSpace(msg)
	return msg
}

func run(inv *serpent.Invocation, opts runOptions) error {
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}

	if opts.ref != "" && opts.amend {
		return errors.New("cannot use both [ref] and --amend")
	}

	hash := ""
	if opts.amend {
		lastCommitHash, err := getLastCommitHash()
		if err != nil {
			return err
		}
		hash = lastCommitHash
	} else if opts.ref != "" {
		// Resolve the ref to a hash.
		hash, err = resolveRef(opts.ref)
		if err != nil {
			return fmt.Errorf("resolve ref %q: %w", opts.ref, err)
		}
	}

	msgs, err := aicommit.BuildPrompt(inv.Stdout, workdir, hash, opts.amend, 128000)
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
			debugf("%s: (%v tokens)\n %s\n\n", msg.Role, aicommit.CountTokens(msg), msg.Content)
		}
		debugf("prompt includes %d commits\n", len(msgs)/2)
	}

	stream, err := opts.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:       opts.model,
		Stream:      true,
		Temperature: 0,
		// Seed must not be set for the amend-retry workflow.
		// Seed:        &seed,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
		Messages: msgs,
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	msg := &bytes.Buffer{}

	// Sky blue color
	color := pretty.FgColor(colorProfile.Color("#2FA8FF"))

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				debugf("stream EOF")
				break
			}
			return err
		}
		// Usage is only sent in the last message.
		if resp.Usage != nil {
			debugf("total tokens: %d", resp.Usage.TotalTokens)
			break
		}
		c := resp.Choices[0].Delta.Content
		msg.WriteString(c)
		pretty.Fprintf(inv.Stdout, color, "%s", c)
	}
	inv.Stdout.Write([]byte("\n"))

	msg = bytes.NewBufferString(cleanAIMessage(msg.String()))

	cmd := exec.Command("git", "commit", "-m", msg.String())
	if opts.amend {
		cmd.Args = append(cmd.Args, "--amend")
	}

	if opts.dryRun {
		fmt.Fprintf(inv.Stdout, "Run the following command to commit:\n")
		inv.Stdout.Write([]byte("" + formatShellCommand(cmd) + "\n"))
		return nil
	}
	if opts.ref != "" {
		debugf("targetting old ref, not committing")
		return nil
	}

	inv.Stdout.Write([]byte("\n"))

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

type runOptions struct {
	client  *openai.Client
	model   string
	dryRun  bool
	amend   bool
	ref     string
	context []string
}

func main() {
	var (
		opts         runOptions
		cliOpenAIKey string
		doSaveKey    bool
	)
	cmd := &serpent.Command{
		Use:   "aicommit [ref]",
		Short: "aicommit is a tool for generating commit messages",
		Handler: func(inv *serpent.Invocation) error {
			savedKey, err := loadKey()
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			var openAIKey string
			if savedKey != "" && cliOpenAIKey == "" {
				openAIKey = savedKey
			} else if cliOpenAIKey != "" {
				openAIKey = cliOpenAIKey
			}

			if savedKey != "" && cliOpenAIKey != "" {
				openAIKeyOpt := inv.Command.Options.ByName("openai-key")
				if openAIKeyOpt == nil {
					panic("openai-key option not found")
				}
				// savedKey overrides cliOpenAIKey only when set via environment.
				// See https://github.com/coder/aicommit/issues/6.
				if openAIKeyOpt.ValueSource == serpent.ValueSourceEnv {
					openAIKey = savedKey
				}
			}

			if openAIKey == "" {
				return errors.New("$OPENAI_API_KEY is not set")
			}

			if doSaveKey {
				err := saveKey(cliOpenAIKey)
				if err != nil {
					return err
				}

				kp, err := keyPath()
				if err != nil {
					return err
				}

				fmt.Fprintf(inv.Stdout, "Saved OpenAI API key to %s\n", kp)
				return nil
			}

			client := openai.NewClient(openAIKey)
			opts.client = client
			if len(inv.Args) > 0 {
				opts.ref = inv.Args[0]
			}
			return run(inv, opts)
		},
		Options: []serpent.Option{
			{
				Name:        "openai-key",
				Description: "The OpenAI API key to use.",
				Env:         "OPENAI_API_KEY",
				Flag:        "openai-key",
				Value:       serpent.StringOf(&cliOpenAIKey),
			},
			{
				Name:          "model",
				Description:   "The model to use, e.g. gpt-4o or gpt-4o-mini.",
				Flag:          "model",
				FlagShorthand: "m",
				Default:       "gpt-4o-2024-08-06",
				Env:           "AICOMMIT_MODEL",
				Value:         serpent.StringOf(&opts.model),
			},
			{
				Name:        "save-key",
				Description: "Save the OpenAI API key to persistent local configuration and exit.",
				Flag:        "save-key",
				Value:       serpent.BoolOf(&doSaveKey),
			},
			{
				Name:          "dry-run",
				Flag:          "dry",
				FlagShorthand: "d",
				Description:   "Dry run the command.",
				Value:         serpent.BoolOf(&opts.dryRun),
			},
			{
				Name:          "amend",
				Flag:          "amend",
				FlagShorthand: "a",
				Description:   "Amend the last commit.",
				Value:         serpent.BoolOf(&opts.amend),
			},
			{
				Name:          "context",
				Description:   "Extra context beyond the diff to consider when generating the commit message.",
				Flag:          "context",
				FlagShorthand: "c",
				Value:         serpent.StringArrayOf(&opts.context),
			},
		},
		Children: []*serpent.Command{
			versionCmd(),
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

		errorf("%s\n", err)
		os.Exit(1)
	}
}
