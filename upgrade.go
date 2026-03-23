package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// checkForUpdate checks GitHub for a newer release and prints a warning if available.
// Never blocks startup — silently returns on any error.
func checkForUpdate(currentVersion string) {
	if currentVersion == "dev" || currentVersion == "" {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/iotexproject/ioswarm-agent/releases/latest")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	if semverLessThan(current, latest) {
		fmt.Printf("\n  ⚡ Update available: v%s → v%s\n", current, latest)
		fmt.Printf("     Upgrade: curl -sSL https://github.com/iotexproject/ioswarm-agent/releases/latest/download/ioswarm-agent-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/') -o ioswarm-agent && chmod +x ioswarm-agent\n\n")
	}
}

// semverLessThan returns true if a < b (simple major.minor.patch comparison)
func semverLessThan(a, b string) bool {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return true
		}
		if ap[i] > bp[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	var parts [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		if i >= 3 {
			break
		}
		parts[i], _ = strconv.Atoi(s)
	}
	return parts
}
