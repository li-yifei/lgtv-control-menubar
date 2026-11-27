package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/li-yifei/lgtv-control-menubar/internal/tv"
	"github.com/spf13/cobra"
)

var legacyCommands = map[string][]string{
	"--status": {"status", "--json"}, "--inputs": {"inputs", "--json"}, "--pair": {"pair"},
	"--set-volume": {"volume", "set"}, "--volume-up": {"volume", "up"}, "--volume-down": {"volume", "down"},
	"--toggle-mute": {"mute", "toggle"}, "--power-on": {"power", "on"}, "--power-off": {"power", "off"},
	"--set-input": {"input", "switch"},
}

func cliArgs(args []string, invokedAsCLI bool) ([]string, bool) {
	index := 0
	for index < len(args) {
		switch {
		case args[index] == "--config" && index+1 < len(args):
			index += 2
		case strings.HasPrefix(args[index], "--config="):
			index++
		default:
			goto command
		}
	}
command:
	if index == len(args) {
		return args, invokedAsCLI || index > 0
	}
	name := args[index]
	if name == "--cli" {
		return cliArgs(append(args[:index:index], args[index+1:]...), true)
	}
	if legacy, ok := legacyCommands[name]; ok {
		normalized := append(args[:index:index], legacy...)
		return append(normalized, args[index+1:]...), true
	}
	switch name {
	case "status", "inputs", "pair", "volume", "mute", "power", "input", "raw", "config", "app",
		"--help", "-h", "--version", "-v", "help":
		return args, true
	}
	return args, invokedAsCLI
}

func runCLI(args []string) error {
	root := newCLI()
	root.SetArgs(args)
	return root.Execute()
}

type cliOptions struct {
	config string
}

func (opts *cliOptions) controller() tv.Controller {
	return tv.New(opts.config, keychain{})
}

func newCLI() *cobra.Command {
	opts := cliOptions{}
	root := &cobra.Command{
		Use: "lgtv", Short: "Control a paired LG webOS TV from the terminal",
		Version: "0.6.0", SilenceErrors: true, SilenceUsage: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVar(&opts.config, "config", "", "Pairing JSON path (default LG_TV_CONFIG or ~/.config/lgtv-pairing.json)")

	status := opts.stateCommand("status", "Print TV host, model, volume, mute state, and current app",
		func(out io.Writer, value tv.State, jsonOutput bool) error {
			if jsonOutput {
				return printJSON(out, value)
			}
			_, err := fmt.Fprintf(out, "Host: %s\nModel: %s\nVolume: %s\nMuted: %s\nCurrent App: %s\n",
				value.Host, stringOrDash(value.Model), numberOrDash(value.Volume), boolOrDash(value.Muted), stringOrDash(value.CurrentAppID))
			return err
		})

	volume := &cobra.Command{Use: "volume", Short: "Read or change TV volume"}
	volume.AddCommand(opts.stateCommand("get", "Print current TV volume", func(out io.Writer, s tv.State, jsonOutput bool) error {
		if jsonOutput {
			return printJSON(out, map[string]any{"volume": s.Volume, "muted": s.Muted})
		}
		_, err := fmt.Fprintln(out, numberOrDash(s.Volume))
		return err
	}), opts.mutation("set VOLUME", "Set the TV volume", cobra.ExactArgs(1), func(ctx context.Context, args []string) error {
		value, err := strconv.Atoi(args[0])
		if err != nil || value < 0 || value > 100 {
			return errors.New("volume must be between 0 and 100")
		}
		return opts.controller().SetVolume(ctx, value)
	}), opts.volumeSteps("up", true), opts.volumeSteps("down", false))

	mute := &cobra.Command{Use: "mute", Short: "Read or change TV mute state"}
	mute.AddCommand(opts.stateCommand("status", "Print current mute state", func(out io.Writer, s tv.State, jsonOutput bool) error {
		if jsonOutput {
			return printJSON(out, map[string]any{"muted": s.Muted})
		}
		_, err := fmt.Fprintln(out, boolOrDash(s.Muted))
		return err
	}), opts.mutation("toggle", "Toggle mute", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return opts.controller().ToggleMute(ctx)
	}), opts.mutation("on", "Mute the TV", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return opts.controller().SetMute(ctx, true)
	}), opts.mutation("off", "Unmute the TV", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return opts.controller().SetMute(ctx, false)
	}))

	power := &cobra.Command{Use: "power", Short: "Turn the TV on or off"}
	power.AddCommand(opts.mutation("on", "Wake the TV using Wake-on-LAN", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return opts.controller().PowerOn(ctx)
	}), opts.mutation("off", "Turn the TV off", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return opts.controller().PowerOff(ctx)
	}))

	input := &cobra.Command{Use: "input", Short: "List, inspect, or switch TV inputs"}
	input.AddCommand(opts.inputs("list"), opts.stateCommand("current", "Print current foreground app ID", func(out io.Writer, s tv.State, jsonOutput bool) error {
		if jsonOutput {
			return printJSON(out, map[string]any{"currentAppId": s.CurrentAppID})
		}
		_, err := fmt.Fprintln(out, stringOrDash(s.CurrentAppID))
		return err
	}), opts.mutation("switch INPUT", "Switch to a TV input", cobra.ExactArgs(1), func(ctx context.Context, args []string) error {
		return opts.controller().SwitchInput(ctx, args[0])
	}))

	config := &cobra.Command{Use: "config", Short: "Inspect CLI configuration"}
	config.AddCommand(&cobra.Command{Use: "path", Short: "Print the pairing config path", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), tv.ConfigPath(opts.config))
			return err
		},
	})

	app := &cobra.Command{Use: "app", Short: "Open the menu bar app"}
	app.AddCommand(opts.mutation("open", "Open LG TV Control", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return openApp(ctx, false)
	}), opts.mutation("settings", "Open LG TV Control settings", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		return openApp(ctx, true)
	}))

	root.AddCommand(status, opts.inputs("inputs"), opts.pair(), volume, mute, power, input, opts.raw(), config, app)
	return root
}

func (opts *cliOptions) pair() *cobra.Command {
	var reuseKey, quiet bool
	cmd := &cobra.Command{Use: "pair [HOST]", Short: "Pair the TV and save its client key", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			controller := opts.controller()
			host := controller.ConfiguredHost()
			if len(args) == 1 {
				host = args[0]
			}
			if host == "" {
				return errors.New("missing host. Use lgtv pair <host>")
			}
			if err := controller.Pair(cmd.Context(), host, !reuseKey); err != nil {
				return err
			}
			return writeResult(cmd, "paired "+host, quiet)
		},
	}
	cmd.Flags().BoolVar(&reuseKey, "reuse-key", false, "Try the saved client key")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress success output")
	return cmd
}

func (opts *cliOptions) mutation(use, description string, args cobra.PositionalArgs, action func(context.Context, []string) error) *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{Use: use, Short: description, Args: args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := action(cmd.Context(), args); err != nil {
				return err
			}
			return writeResult(cmd, "ok", quiet)
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress success output")
	return cmd
}

func (opts *cliOptions) volumeSteps(use string, up bool) *cobra.Command {
	var steps int
	cmd := opts.mutation(use, "Adjust TV volume by one or more steps", cobra.NoArgs, func(ctx context.Context, _ []string) error {
		if steps < 1 {
			return errors.New("steps must be at least 1")
		}
		return opts.controller().VolumeSteps(ctx, up, steps)
	})
	cmd.Flags().IntVarP(&steps, "steps", "s", 1, "Number of one-step adjustments")
	return cmd
}

func (opts *cliOptions) stateCommand(use, description string, print func(io.Writer, tv.State, bool) error) *cobra.Command {
	return outputCommand(use, description, func(cmd *cobra.Command, jsonOutput bool) error {
		state, err := opts.controller().Status(cmd.Context())
		if err != nil {
			return err
		}
		return print(cmd.OutOrStdout(), state, jsonOutput)
	})
}

func (opts *cliOptions) inputs(use string) *cobra.Command {
	return outputCommand(use, "List TV input sources", func(cmd *cobra.Command, jsonOutput bool) error {
		inputs, err := opts.controller().InputList(cmd.Context())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if jsonOutput {
			return printJSON(out, inputs)
		}
		fmt.Fprintln(out, "ID\tLABEL\tAPP_ID\tCONNECTED\tSIGNAL")
		for _, input := range inputs {
			fmt.Fprintf(out, "%s\t%s\t%s\t%t\t%t\n", input.ID, input.Label, stringOrDash(input.AppID), input.Connected, input.HasSignal)
		}
		return nil
	})
}

func outputCommand(use, description string, run func(*cobra.Command, bool) error) *cobra.Command {
	var format string
	var jsonOutput bool
	cmd := &cobra.Command{Use: use, Short: description, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !strings.EqualFold(format, "text") && !strings.EqualFold(format, "json") {
				return errors.New("format must be text or json")
			}
			return run(cmd, jsonOutput || strings.EqualFold(format, "json"))
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format (text or json)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	return cmd
}

func (opts *cliOptions) raw() *cobra.Command {
	var payload string
	cmd := &cobra.Command{Use: "raw URI", Short: "Send a raw webOS SSAP request", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var object map[string]any
			if cmd.Flags().Changed("payload") {
				if err := json.Unmarshal([]byte(payload), &object); err != nil || object == nil {
					return errors.New("payload must be a JSON object")
				}
			}
			result, err := opts.controller().Raw(cmd.Context(), args[0], object)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVarP(&payload, "payload", "p", "", "JSON object payload")
	cmd.Flags().Bool("json", false, "Write JSON output (the default)")
	return cmd
}

func printJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeResult(cmd *cobra.Command, value string, quiet bool) error {
	if quiet {
		return nil
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), value)
	return err
}

func stringOrDash(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}

func numberOrDash(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

func boolOrDash(value *bool) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatBool(*value)
}

func openApp(ctx context.Context, settings bool) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Dir(executable)
	bundle := filepath.Dir(filepath.Dir(dir))
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(filepath.Dir(dir), "LG TV Control.app"),
		"/Applications/LG TV Control.app",
		filepath.Join(home, "Applications", "LG TV Control.app"),
	}
	if filepath.Base(dir) == "MacOS" && filepath.Ext(bundle) == ".app" {
		candidates = append([]string{bundle}, candidates...)
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			args := []string{path}
			if settings {
				args = []string{"-n", path, "--args", "--show-settings"}
			}
			return exec.CommandContext(ctx, "/usr/bin/open", args...).Run()
		}
	}
	return errors.New("could not find LG TV Control.app")
}
