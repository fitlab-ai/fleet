package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fitlab-ai/fleet/internal/app"
	"github.com/fitlab-ai/fleet/internal/credential"
	"github.com/fitlab-ai/fleet/internal/platform"
)

type Dependencies struct {
	App *app.App
	In  io.Reader
	Out io.Writer
}

const helpTemplate = `fleet — CLI proxy node manager backed by sing-box.

Two modes:
  proxy   HTTP/SOCKS on 127.0.0.1:%d   (also sets macOS system proxy)
  tun     System-wide TUN + fake-IP DNS  (needs sudo, Docker auto-proxied)

Commands:
  subscription add [NAME] [URL]  Add a named or auto-named subscription
  subscription status [NAME|ID]  Show redacted subscription status
  subscription remove NAME|ID    Remove credential, retain cache until refresh
  subscription migrate            Import FlClash snapshot (use --name/--url)
  refresh [NAME|ID] [--force]     Refresh one or all active subscriptions
  list                            List aggregated nodes and their sources
  ping [name|index|@source/node]  Test direct TCP port reachability only
  health [name|index|@source/node] Test an HTTPS request via Fleet outbound

  proxy start <name|index>  Start proxy mode
  proxy stop                Stop proxy mode
  proxy switch <name|index> Switch node (proxy mode)
  proxy status              Shortcut: status

  tun start <name|index>    Start TUN mode (needs sudo)
  tun stop                  Stop TUN mode
  tun switch <name|index>   Switch node (TUN mode)
  tun status                Shortcut: status

  start <name|index>        Start (default: proxy mode)
  stop                      Stop whatever is running
  status                    Show current state
  switch <name|index>       Switch in active mode

  export <name|index>       Export sing-box config (proxy mode)
  export tun <name|index>   Export sing-box config (TUN mode)

Config dir: ~/.config/fleet/
Backend:    sing-box (brew install sing-box)
`

func makeApp(deps Dependencies) *app.App {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.In == nil {
		deps.In = os.Stdin
	}
	if deps.App != nil {
		deps.App.Out, deps.App.In = deps.Out, deps.In
		return deps.App
	}
	runner := platform.ExecRunner{}
	return &app.App{
		Config: app.DefaultConfig(), Credentials: credential.NewKeychain(runner),
		Out: deps.Out, In: deps.In,
	}
}

func Run(args []string, deps Dependencies) int {
	a := makeApp(deps)
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(a.Out, helpTemplate, a.Config.Port)
		return 0
	}
	command := args[0]
	switch command {
	case "subscription":
		return subscriptionCommand(a, args[1:])
	case "refresh":
		var selector string
		force := false
		for _, arg := range args[1:] {
			if arg == "--force" {
				force = true
			} else if selector == "" {
				selector = arg
			} else {
				fmt.Fprintln(a.Out, "Try: fleet refresh [NAME|ID] [--force]")
				return 1
			}
		}
		return a.Refresh(selector, force)
	case "list", "ls":
		return a.List()
	case "export":
		if len(args) > 1 && args[1] == "tun" {
			target := ""
			if len(args) > 2 {
				target = args[2]
			}
			return a.Export(target, "tun")
		}
		target := ""
		if len(args) > 1 {
			target = args[1]
		}
		return a.Export(target, "proxy")
	case "proxy", "tun":
		return modeCommand(a, command, args[1:])
	case "start":
		return a.Start(arg(args, 1), "proxy")
	case "stop":
		return a.Stop()
	case "status", "st":
		return a.Status()
	case "switch", "sw":
		return a.Switch(arg(args, 1), "")
	case "ping", "bench":
		return a.Ping(optional(args, 1))
	case "health":
		return a.Health(optional(args, 1))
	default:
		fmt.Fprintf(a.Out, "Unknown command: %s\nTry: fleet help\n", command)
		return 1
	}
}

func arg(args []string, index int) string {
	if index < len(args) {
		return args[index]
	}
	return ""
}
func optional(args []string, index int) string { return arg(args, index) }

func subscriptionCommand(a *app.App, args []string) int {
	sub := arg(args, 0)
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}
	switch sub {
	case "add":
		if len(rest) > 2 {
			fmt.Fprintln(a.Out, "Try: fleet subscription add [NAME] [URL]")
			return 1
		}
		if len(rest) == 2 {
			return a.SubscriptionAdd(rest[0], rest[1])
		}
		if len(rest) == 1 && strings.HasPrefix(strings.ToLower(rest[0]), "https://") {
			return a.SubscriptionAdd("", rest[0])
		}
		return a.SubscriptionAdd(arg(rest, 0), "")
	case "status":
		if len(rest) > 1 {
			fmt.Fprintln(a.Out, "Try: fleet subscription status [NAME|ID]")
			return 1
		}
		return a.SubscriptionStatus(arg(rest, 0))
	case "remove":
		if len(rest) != 1 {
			fmt.Fprintln(a.Out, "Try: fleet subscription remove NAME|ID")
			return 1
		}
		return a.SubscriptionRemove(rest[0])
	case "migrate":
		source, rawURL, name := a.Config.ClashConfig, "", ""
		for i := 0; i < len(rest); {
			if i+1 >= len(rest) {
				fmt.Fprintln(a.Out, "Try: fleet subscription migrate [--name NAME] [--url URL] [--source PATH]")
				return 1
			}
			switch rest[i] {
			case "--source":
				source = rest[i+1]
			case "--url":
				rawURL = rest[i+1]
			case "--name":
				name = rest[i+1]
			default:
				fmt.Fprintln(a.Out, "Try: fleet subscription migrate [--name NAME] [--url URL] [--source PATH]")
				return 1
			}
			i += 2
		}
		return a.SubscriptionMigrate(filepath.Clean(source), rawURL, name)
	default:
		fmt.Fprintln(a.Out, "Try: fleet subscription add|status|remove|migrate")
		return 1
	}
}

func modeCommand(a *app.App, mode string, args []string) int {
	sub := arg(args, 0)
	target := arg(args, 1)
	switch sub {
	case "start":
		return a.Start(target, mode)
	case "stop":
		return a.Stop()
	case "switch", "sw":
		return a.Switch(target, mode)
	case "status", "st":
		return a.Status()
	default:
		fmt.Fprintf(a.Out, "Unknown %s subcommand: %s\nTry: fleet %s start|stop|switch|status\n", mode, sub, mode)
		return 1
	}
}
