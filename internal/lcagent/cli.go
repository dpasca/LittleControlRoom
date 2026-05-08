package lcagent

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	skillcatalog "lcroom/internal/lcagent/skills"
	"lcroom/internal/lcagent/tools"
)

const version = "dev"

type outputMode string

const (
	outputText       outputMode = "text"
	outputJSON       outputMode = "json"
	outputStreamJSON outputMode = "stream-json"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: lcagent exec [flags] <prompt>")
		return 2
	}
	switch args[0] {
	case "exec":
		if err := runExec(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, "usage: lcagent exec [flags] <prompt>")
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func runExec(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cwd, dataDir, autoRaw, outputRaw, scriptPath, provider, model, envFile string
	var maxTurns int
	fs.StringVar(&cwd, "cwd", "", "workspace root")
	fs.StringVar(&dataDir, "data-dir", "", "artifact data root")
	fs.StringVar(&autoRaw, "auto", "off", "autonomy: off, low, medium")
	fs.StringVar(&outputRaw, "output", string(outputStreamJSON), "output: text, json, stream-json")
	fs.StringVar(&scriptPath, "script", "", "scripted JSONL actions")
	fs.StringVar(&provider, "provider", "scripted", "provider: scripted or openrouter")
	fs.StringVar(&model, "model", "", "model name")
	fs.StringVar(&envFile, "env-file", "", "optional dotenv file for provider credentials")
	fs.IntVar(&maxTurns, "max-turns", 16, "maximum model turns for provider loops")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	auto, err := policy.ParseAutonomy(autoRaw)
	if err != nil {
		return err
	}
	workspace, err := policy.NewWorkspace(cwd, auto)
	if err != nil {
		return err
	}
	catalog, err := skillcatalog.Discover(context.Background(), skillcatalog.DefaultOptions(workspace.Root))
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	outMode := outputMode(strings.TrimSpace(outputRaw))
	if outMode != outputText && outMode != outputJSON && outMode != outputStreamJSON {
		return fmt.Errorf("unsupported output mode: %s", outputRaw)
	}
	if strings.TrimSpace(model) == "" {
		if provider == "openrouter" {
			model = modeladapter.DefaultOpenRouterModel
		} else {
			model = "scripted"
		}
	}
	stream := io.Writer(nil)
	if outMode == outputStreamJSON {
		stream = stdout
	}
	started := time.Now()
	writer, sessionID, err := session.NewWriter(dataDir, started, stream)
	if err != nil {
		return err
	}
	defer writer.Close()
	if err := writer.Write(session.Meta(sessionID, workspace.Root, string(auto), provider, model, version, started)); err != nil {
		return err
	}
	if len(catalog.Skills) > 0 {
		if err := writer.Write(session.Event{
			"type":       "skill_catalog",
			"session_id": sessionID,
			"count":      len(catalog.Skills),
			"skills":     catalog.EventSkills(40),
		}); err != nil {
			return err
		}
	}

	artifactDir := filepath.Join(filepath.Dir(writer.Path()), sessionID+"-artifacts")
	runner := script.Runner{
		Session:      writer,
		Command:      tools.CommandRunner{Workspace: workspace, ArtifactDir: artifactDir},
		Patch:        tools.PatchApplier{Workspace: workspace},
		Files:        tools.FileTools{Workspace: workspace},
		Skills:       catalog,
		SessionID:    sessionID,
		Prompt:       prompt,
		ArtifactsDir: artifactDir,
	}

	var runErr error
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "scripted":
		if scriptPath == "" {
			return fmt.Errorf("--script is required for scripted provider")
		}
		actions, err := script.Load(scriptPath)
		if err != nil {
			return err
		}
		runErr = runner.Run(context.Background(), actions)
	case "openrouter":
		runErr = runOpenRouter(context.Background(), writer, runner, modeladapter.OpenRouterConfig{
			Model:    model,
			EnvFile:  envFile,
			MaxTurns: maxTurns,
		})
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
	if runErr != nil {
		return runErr
	}
	if outMode == outputText {
		fmt.Fprintf(stdout, "Session %s complete\nArtifact: %s\n", sessionID, writer.Path())
	}
	if outMode == outputJSON {
		return json.NewEncoder(stdout).Encode(map[string]any{
			"session_id": sessionID,
			"artifact":   writer.Path(),
			"status":     "complete",
		})
	}
	return nil
}

func runOpenRouter(ctx context.Context, writer *session.Writer, runner script.Runner, cfg modeladapter.OpenRouterConfig) error {
	client, err := modeladapter.NewOpenRouterClient(cfg)
	if err != nil {
		_ = writer.Write(session.Event{
			"type":       "turn_aborted",
			"session_id": runner.SessionID,
			"reason":     err.Error(),
		})
		return err
	}
	if err := writer.Write(session.Event{
		"type":       "user_message",
		"session_id": runner.SessionID,
		"message":    runner.Prompt,
	}); err != nil {
		return err
	}

	messages := []modeladapter.Message{
		{Role: "system", Content: modeladapter.SystemPrompt(runner.Skills.PromptIndex(0))},
		{Role: "user", Content: runner.Prompt},
	}
	toolsDef := modeladapter.Tools()
	for turn := 0; turn < client.MaxTurns(); turn++ {
		completion, err := client.Complete(ctx, messages, toolsDef)
		if err != nil {
			_ = writer.Write(session.Event{
				"type":       "turn_aborted",
				"session_id": runner.SessionID,
				"reason":     err.Error(),
			})
			return err
		}
		msg := completion.Message
		if err := writer.Write(modelResponseEvent(runner.SessionID, turn+1, completion, len(msg.ToolCalls))); err != nil {
			return err
		}
		messages = append(messages, msg)
		if msg.Content != "" {
			if err := writer.Write(session.Event{
				"type":       "assistant_message",
				"session_id": runner.SessionID,
				"message":    msg.Content,
			}); err != nil {
				return err
			}
		}
		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				err := fmt.Errorf("openrouter response had no content or tool calls")
				_ = writer.Write(session.Event{
					"type":       "turn_aborted",
					"session_id": runner.SessionID,
					"reason":     err.Error(),
				})
				return err
			}
			return runner.Final(script.Action{
				Type:    "final_response",
				Summary: msg.Content,
			})
		}
		for _, call := range msg.ToolCalls {
			args, err := modeladapter.NormalizeArguments(call.Function.Arguments)
			if err != nil {
				return err
			}
			action := script.Action{Type: "tool_call", Tool: call.Function.Name, Args: args}
			if call.Function.Name == "final_response" {
				var final script.Action
				if err := json.Unmarshal(args, &final); err != nil {
					return err
				}
				final.Type = "final_response"
				return runner.Final(final)
			}
			result, err := runner.RunTool(ctx, action)
			resultJSON, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return marshalErr
			}
			messages = append(messages, modeladapter.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    string(resultJSON),
			})
			if err != nil {
				// Feed tool failures back to the model once; the structured event has
				// already recorded the failure for LCR.
				continue
			}
		}
	}
	err = fmt.Errorf("openrouter model loop exceeded maximum turns")
	_ = writer.Write(session.Event{
		"type":       "turn_aborted",
		"session_id": runner.SessionID,
		"reason":     err.Error(),
	})
	return err
}

func modelResponseEvent(sessionID string, turn int, completion modeladapter.Completion, toolCallCount int) session.Event {
	event := session.Event{
		"type":            "model_response",
		"session_id":      sessionID,
		"turn":            turn,
		"model":           completion.Model,
		"tool_call_count": toolCallCount,
	}
	if completion.ID != "" {
		event["response_id"] = completion.ID
	}
	if completion.FinishReason != "" {
		event["finish_reason"] = completion.FinishReason
	}
	if len(completion.Usage) > 0 && string(completion.Usage) != "null" {
		event["usage"] = json.RawMessage(completion.Usage)
	}
	return event
}

func defaultDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("LCROOM_DATA_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".little-control-room")
}
