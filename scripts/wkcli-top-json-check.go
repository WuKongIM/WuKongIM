//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty alert key")
	}
	if !strings.Contains(value, "/") {
		return fmt.Errorf("alert key must be component/kind: %s", value)
	}
	*m = append(*m, value)
	return nil
}

type topAggregate struct {
	ReadyNodes int       `json:"ready_nodes"`
	TotalNodes int       `json:"total_nodes"`
	Nodes      []topNode `json:"nodes"`
}

type topNode struct {
	Node      topNodeIdentity `json:"node"`
	Resources *topResources   `json:"resources"`
	Alerts    *topAlerts      `json:"alerts"`
}

type topNodeIdentity struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
}

type topResources struct {
	MemoryRSSBytes uint64 `json:"memory_rss_bytes"`
	Goroutines     int    `json:"goroutines"`
}

type topAlerts struct {
	Active []topAlert `json:"active"`
}

type topAlert struct {
	Severity  string `json:"severity"`
	Component string `json:"component"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
}

type benchSummary struct {
	Result  string `json:"result"`
	Success uint64 `json:"success"`
	Errors  uint64 `json:"errors"`
	Traffic struct {
		Messages int `json:"messages"`
	} `json:"traffic"`
}

func main() {
	var topPath string
	var benchPath string
	var wantNodes int
	var wantBenchMessages int
	var requireReady bool
	var requireResources bool
	var forbidAlerts multiFlag
	var requireAlerts multiFlag
	flag.StringVar(&topPath, "top", "", "wkcli top aggregate JSON file")
	flag.StringVar(&benchPath, "bench", "", "wkcli bench send JSON file")
	flag.IntVar(&wantNodes, "want-nodes", 0, "expected top node count")
	flag.IntVar(&wantBenchMessages, "want-bench-messages", 0, "expected bench message count")
	flag.BoolVar(&requireReady, "require-ready", false, "require all top nodes to be ready")
	flag.BoolVar(&requireResources, "require-resources", false, "require process resources for every top node")
	flag.Var(&forbidAlerts, "forbid-alert", "fail if an active component/kind alert exists")
	flag.Var(&requireAlerts, "require-alert", "fail if an active component/kind alert is missing")
	flag.Parse()

	var failures []string
	if topPath != "" {
		topFailures := checkTop(topPath, wantNodes, requireReady, requireResources, forbidAlerts, requireAlerts)
		if len(topFailures) == 0 {
			fmt.Println("top: passed")
		} else {
			fmt.Println("top: failed")
			failures = append(failures, topFailures...)
		}
	}
	if benchPath != "" {
		benchFailures := checkBench(benchPath, wantBenchMessages)
		if len(benchFailures) == 0 {
			fmt.Println("bench: passed")
		} else {
			fmt.Println("bench: failed")
			failures = append(failures, benchFailures...)
		}
	}
	for _, failure := range failures {
		fmt.Println(failure)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
}

func checkTop(path string, wantNodes int, requireReady bool, requireResources bool, forbidAlerts []string, requireAlerts []string) []string {
	var failures []string
	var doc topAggregate
	if err := readJSON(path, &doc); err != nil {
		return []string{"top_json: " + err.Error()}
	}
	if wantNodes > 0 {
		if doc.TotalNodes != wantNodes {
			failures = append(failures, fmt.Sprintf("total_nodes: got=%d want=%d", doc.TotalNodes, wantNodes))
		}
		if len(doc.Nodes) != wantNodes {
			failures = append(failures, fmt.Sprintf("nodes: got=%d want=%d", len(doc.Nodes), wantNodes))
		}
	}
	if requireReady {
		if doc.ReadyNodes != doc.TotalNodes {
			failures = append(failures, fmt.Sprintf("ready_nodes: got=%d total=%d", doc.ReadyNodes, doc.TotalNodes))
		}
		for i, node := range doc.Nodes {
			if !node.Node.Ready {
				failures = append(failures, fmt.Sprintf("node_ready[%d]: false", i))
			}
		}
	}
	if requireResources {
		for i, node := range doc.Nodes {
			if node.Resources == nil {
				failures = append(failures, fmt.Sprintf("resources[%d]: missing", i))
				continue
			}
			if node.Resources.MemoryRSSBytes == 0 {
				failures = append(failures, fmt.Sprintf("resources[%d].memory_rss_bytes: zero", i))
			}
			if node.Resources.Goroutines == 0 {
				failures = append(failures, fmt.Sprintf("resources[%d].goroutines: zero", i))
			}
		}
	}
	for _, key := range forbidAlerts {
		alert, ok := findActiveAlert(doc, key)
		if ok {
			failures = append(failures, fmt.Sprintf("forbid_alert %s: active node=%s severity=%s message=%s", key, alert.nodeName, alert.alert.Severity, alert.alert.Message))
		} else {
			fmt.Printf("forbid_alert %s: ok\n", key)
		}
	}
	for _, key := range requireAlerts {
		alert, ok := findActiveAlert(doc, key)
		if !ok {
			failures = append(failures, fmt.Sprintf("require_alert %s: missing", key))
		} else {
			fmt.Printf("require_alert %s: active node=%s severity=%s message=%s\n", key, alert.nodeName, alert.alert.Severity, alert.alert.Message)
		}
	}
	return failures
}

type matchedAlert struct {
	nodeName string
	alert    topAlert
}

func findActiveAlert(doc topAggregate, key string) (matchedAlert, bool) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return matchedAlert{}, false
	}
	for _, node := range doc.Nodes {
		if node.Alerts == nil {
			continue
		}
		nodeName := node.Node.Name
		if nodeName == "" {
			nodeName = "unknown"
		}
		for _, alert := range node.Alerts.Active {
			if alert.Component == parts[0] && alert.Kind == parts[1] {
				return matchedAlert{nodeName: nodeName, alert: alert}, true
			}
		}
	}
	return matchedAlert{}, false
}

func checkBench(path string, wantMessages int) []string {
	var failures []string
	var doc benchSummary
	if err := readJSON(path, &doc); err != nil {
		return []string{"bench_json: " + err.Error()}
	}
	if doc.Result != "pass" {
		failures = append(failures, "bench_result: "+doc.Result)
	}
	if doc.Errors != 0 {
		failures = append(failures, fmt.Sprintf("bench_errors: %d", doc.Errors))
	}
	if doc.Success == 0 {
		failures = append(failures, "bench_success: zero")
	}
	if wantMessages > 0 {
		if int(doc.Success) != wantMessages {
			failures = append(failures, fmt.Sprintf("bench_success: got=%d want=%d", doc.Success, wantMessages))
		}
		if doc.Traffic.Messages != wantMessages {
			failures = append(failures, fmt.Sprintf("bench_messages: got=%d want=%d", doc.Traffic.Messages, wantMessages))
		}
	}
	return failures
}

func readJSON(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(out); err != nil {
		return err
	}
	return nil
}
