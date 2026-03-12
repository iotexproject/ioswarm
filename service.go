package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"text/template"
)

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.iotex.ioswarm</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.AgentBin}}</string>
    <string>--coordinator={{.Coordinator}}</string>
    <string>--agent-id={{.AgentID}}</string>
    <string>--level={{.Level}}</string>
    <string>--region={{.Region}}</string>
    <string>--wallet={{.Wallet}}</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    {{- if .APIKey}}
    <key>IOSWARM_API_KEY</key>
    <string>{{.APIKey}}</string>
    {{- end}}
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>StandardOutPath</key>
  <string>{{.LogFile}}</string>
  <key>StandardErrorPath</key>
  <string>{{.LogFile}}</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
</dict>
</plist>
`

const systemdUnit = `[Unit]
Description=ioSwarm Agent — IoTeX transaction validator
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.AgentBin}} \
  --coordinator={{.Coordinator}} \
  --agent-id={{.AgentID}} \
  --level={{.Level}} \
  --region={{.Region}} \
  --wallet={{.Wallet}}
{{- if .APIKey}}
Environment=IOSWARM_API_KEY={{.APIKey}}
{{- end}}
Restart=on-failure
RestartSec=10
StandardOutput=append:{{.LogFile}}
StandardError=append:{{.LogFile}}

[Install]
WantedBy=default.target
`

type serviceConfig struct {
	AgentBin    string
	Coordinator string
	AgentID     string
	Level       string
	Region      string
	Wallet      string
	APIKey      string
	LogFile     string
}

func runService(args []string) {
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	action := fs.String("action", "install", "install or uninstall")
	dataDir := fs.String("datadir", "", "agent data directory (default: ~/.ioswarm/agent)")
	fs.Parse(args)

	dir := *dataDir
	if dir == "" {
		dir = os.Getenv("IOSWARM_DATADIR")
	}
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.ioswarm/agent"
	}

	switch *action {
	case "install":
		installService(dir)
	case "uninstall":
		uninstallService()
	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s (use install or uninstall)\n", *action)
		os.Exit(1)
	}
}

func loadServiceConfig(dir string) serviceConfig {
	cfg := serviceConfig{
		AgentBin: dir + "/ioswarm-agent",
		LogFile:  dir + "/agent.log",
		Level:    "L2",
		Region:   "default",
	}

	if b, err := os.ReadFile(dir + "/config.env"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "IOSWARM_COORDINATOR":
				cfg.Coordinator = parts[1]
			case "IOSWARM_AGENT_ID":
				cfg.AgentID = parts[1]
			case "IOSWARM_LEVEL":
				cfg.Level = parts[1]
			case "IOSWARM_REGION":
				cfg.Region = parts[1]
			case "IOSWARM_WALLET":
				cfg.Wallet = parts[1]
			case "IOSWARM_API_KEY":
				cfg.APIKey = parts[1]
			}
		}
	}

	return cfg
}

func installService(dir string) {
	cfg := loadServiceConfig(dir)

	if cfg.AgentID == "" {
		fmt.Fprintf(os.Stderr, "error: no config found. Run setup first: ioswarm.sh setup\n")
		os.Exit(1)
	}

	switch runtime.GOOS {
	case "darwin":
		installLaunchd(cfg)
	case "linux":
		installSystemd(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unsupported OS: %s (use macOS or Linux)\n", runtime.GOOS)
		os.Exit(1)
	}
}

func installLaunchd(cfg serviceConfig) {
	home, _ := os.UserHomeDir()
	plistDir := home + "/Library/LaunchAgents"
	plistPath := plistDir + "/io.iotex.ioswarm.plist"

	if err := os.MkdirAll(plistDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating LaunchAgents dir: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(plistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating plist: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	tmpl := template.Must(template.New("plist").Parse(launchdPlist))
	if err := tmpl.Execute(f, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing plist: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed: %s\n", plistPath)
	fmt.Println()
	fmt.Println("To start now and enable on boot:")
	fmt.Printf("  launchctl load %s\n", plistPath)
	fmt.Println()
	fmt.Println("To stop and disable:")
	fmt.Printf("  launchctl unload %s\n", plistPath)
}

func installSystemd(cfg serviceConfig) {
	home, _ := os.UserHomeDir()
	unitDir := home + "/.config/systemd/user"
	unitPath := unitDir + "/ioswarm.service"

	if err := os.MkdirAll(unitDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating systemd dir: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(unitPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating unit file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	tmpl := template.Must(template.New("unit").Parse(systemdUnit))
	if err := tmpl.Execute(f, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error writing unit: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed: %s\n", unitPath)
	fmt.Println()
	fmt.Println("To start now and enable on boot:")
	fmt.Println("  systemctl --user daemon-reload")
	fmt.Println("  systemctl --user enable --now ioswarm")
	fmt.Println()
	fmt.Println("To stop and disable:")
	fmt.Println("  systemctl --user disable --now ioswarm")
}

func uninstallService() {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		path := home + "/Library/LaunchAgents/io.iotex.ioswarm.plist"
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("Service not installed.")
			} else {
				fmt.Fprintf(os.Stderr, "error removing plist: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Removed: %s\n", path)
			fmt.Println("Run 'launchctl unload' first if the service is still loaded.")
		}
	case "linux":
		home, _ := os.UserHomeDir()
		path := home + "/.config/systemd/user/ioswarm.service"
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("Service not installed.")
			} else {
				fmt.Fprintf(os.Stderr, "error removing unit: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Removed: %s\n", path)
			fmt.Println("Run 'systemctl --user disable --now ioswarm' first if the service is still running.")
		}
	}
}
