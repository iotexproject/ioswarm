package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type agentStatus struct {
	Running        bool   `json:"running"`
	PID            int    `json:"pid,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	Wallet         string `json:"wallet,omitempty"`
	Coordinator    string `json:"coordinator,omitempty"`
	Level          string `json:"level,omitempty"`
	Region         string `json:"region,omitempty"`
	UptimeSeconds  int64  `json:"uptime_seconds,omitempty"`
	TasksProcessed int    `json:"tasks_processed"`
	LogFile        string `json:"log_file,omitempty"`
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
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

	st := agentStatus{
		LogFile: dir + "/agent.log",
	}

	// Read agent ID
	if b, err := os.ReadFile(dir + "/agent.id"); err == nil {
		st.AgentID = strings.TrimSpace(string(b))
	}

	// Read wallet address
	if b, err := os.ReadFile(dir + "/wallet.addr"); err == nil {
		st.Wallet = strings.TrimSpace(string(b))
	}

	// Read config for coordinator, level, region
	if b, err := os.ReadFile(dir + "/config.env"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "IOSWARM_COORDINATOR":
				st.Coordinator = parts[1]
			case "IOSWARM_LEVEL":
				st.Level = parts[1]
			case "IOSWARM_REGION":
				st.Region = parts[1]
			}
		}
	}

	// Check if agent is running via PID file
	if b, err := os.ReadFile(dir + "/agent.pid"); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil {
			// Check if process is alive
			proc, err := os.FindProcess(pid)
			if err == nil && proc.Signal(syscall.Signal(0)) == nil {
				st.Running = true
				st.PID = pid

				// Estimate uptime from PID file mtime
				if info, err := os.Stat(dir + "/agent.pid"); err == nil {
					st.UptimeSeconds = int64(time.Since(info.ModTime()).Seconds())
				}
			}
		}
	}

	// Count tasks from log
	if b, err := os.ReadFile(dir + "/agent.log"); err == nil {
		st.TasksProcessed = strings.Count(string(b), "received batch")
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding status: %v\n", err)
		os.Exit(1)
	}
}
