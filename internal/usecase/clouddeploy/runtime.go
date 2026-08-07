package clouddeploy

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderedFile is one non-secret host-specific configuration generated from a Plan.
type RenderedFile struct {
	Path    string
	Content []byte
	Mode    uint32
}

// RenderHostFiles renders only Lease-specific non-secret configuration. Runtime
// credentials remain separate root-owned files and are never accepted here.
func RenderHostFiles(plan DeploymentPlan, role string, templates map[string]string) ([]RenderedFile, error) {
	host, ok := findHost(plan.Hosts, role)
	if !ok || plan.Topology != fixedTopology() || len(plan.Hosts) != 4 {
		return nil, ErrInvalidDeployment
	}
	services := plan.Hosts[:ServiceHostCount]
	load, ok := findHost(plan.Hosts, "load")
	if !ok {
		return nil, ErrInvalidDeployment
	}
	if strings.HasPrefix(role, "service-") {
		template := templates["wukongim.toml"]
		if template == "" {
			return nil, ErrInvalidDeployment
		}
		nodes := make([]string, 0, len(services))
		for _, service := range services {
			nodes = append(nodes, fmt.Sprintf(`{ id = %d, addr = %q }`, service.NodeID, service.PrivateAddress+":7000"))
		}
		content := replaceAll(template, map[string]string{
			"{{NODE_ID}}": strconv.Itoa(host.NodeID), "{{PRIVATE_IPV4}}": host.PrivateAddress,
			"{{CLUSTER_NODES}}":    "[" + strings.Join(nodes, ", ") + "]",
			"{{PUBLIC_HTTP_HOST}}": load.PublicAddress, "{{LOAD_PRIVATE_IPV4}}": load.PrivateAddress,
		})
		if strings.Contains(content, "{{") {
			return nil, ErrInvalidDeployment
		}
		return []RenderedFile{{Path: "etc/wukongim/wukongim.toml", Content: []byte(content), Mode: 0o640}}, nil
	}
	if role != "load" {
		return nil, ErrInvalidDeployment
	}
	prometheus := templates["prometheus.yml"]
	caddy := templates["Caddyfile"]
	workload := templates["chat-lifecycle.yaml"]
	rehearsal := templates["chat-lifecycle-rehearsal.yaml"]
	if prometheus == "" || caddy == "" || workload == "" || rehearsal == "" {
		return nil, ErrInvalidDeployment
	}
	apiTargets := make([]string, 0, len(services))
	nodeTargets := make([]string, 0, len(plan.Hosts))
	apiUpstreams := make([]string, 0, len(services))
	wsUpstreams := make([]string, 0, len(services))
	managerUpstreams := make([]string, 0, len(services))
	for _, service := range services {
		apiTargets = append(apiTargets, strconv.Quote(service.PrivateAddress+":5001"))
		apiUpstreams = append(apiUpstreams, service.PrivateAddress+":5001")
		wsUpstreams = append(wsUpstreams, service.PrivateAddress+":5200")
		managerUpstreams = append(managerUpstreams, service.PrivateAddress+":5301")
		nodeTargets = append(nodeTargets, strconv.Quote(service.PrivateAddress+":9100"))
	}
	nodeTargets = append(nodeTargets, strconv.Quote(load.PrivateAddress+":9100"))
	prometheus = replaceAll(prometheus, map[string]string{
		"{{WUKONGIM_METRICS_TARGETS}}": strings.Join(apiTargets, ", "),
		"{{NODE_EXPORTER_TARGETS}}":    strings.Join(nodeTargets, ", "),
	})
	caddy = replaceAll(caddy, map[string]string{
		"{{DEMO_WS_UPSTREAMS}}": strings.Join(wsUpstreams, " "), "{{DEMO_API_UPSTREAMS}}": strings.Join(apiUpstreams, " "),
		"{{MANAGER_UPSTREAMS}}": strings.Join(managerUpstreams, " "),
	})
	workload = renderWorkloadConfig(workload, plan)
	rehearsal = renderWorkloadConfig(rehearsal, plan)
	analysisScenario := fmt.Sprintf("version: wkbench/v1\nrun:\n  id: %s\n  random_seed: 1\n  report_dir: /var/lib/wukongim-cloud/reports\nobjectives:\n  scale: chat-lifecycle-formal\n", plan.LeaseID)
	if strings.Contains(prometheus, "{{") || strings.Contains(caddy, "{{") || strings.Contains(workload, ".invalid") ||
		strings.Contains(rehearsal, ".invalid") || strings.Contains(workload, "replace-with-unique-formal-run-id") ||
		strings.Contains(rehearsal, "replace-with-unique-rehearsal-run-id") {
		return nil, ErrInvalidDeployment
	}
	return []RenderedFile{
		{Path: "etc/wukongim/prometheus.yml", Content: []byte(prometheus), Mode: 0o640},
		{Path: "etc/wukongim/Caddyfile", Content: []byte(caddy), Mode: 0o640},
		{Path: "etc/wukongim/chat-lifecycle.yaml", Content: []byte(workload), Mode: 0o640},
		{Path: "etc/wukongim/chat-lifecycle-rehearsal.yaml", Content: []byte(rehearsal), Mode: 0o640},
		{Path: "etc/wukongim/analysis-scenario.yaml", Content: []byte(analysisScenario), Mode: 0o640},
	}, nil
}

func renderWorkloadConfig(content string, plan DeploymentPlan) string {
	content = strings.Replace(content, "replace-with-unique-formal-run-id", plan.LeaseID, 1)
	content = strings.Replace(content, "replace-with-unique-rehearsal-run-id", plan.LeaseID, 1)
	for index, host := range plan.Hosts[:ServiceHostCount] {
		ordinal := strconv.Itoa(index + 1)
		replacements := map[string]string{
			"http://service-" + ordinal + ".invalid":      "http://" + host.PrivateAddress + ":5001",
			"http://host-metrics-" + ordinal + ".invalid": "http://" + host.PrivateAddress + ":19101",
			"http://api-" + ordinal + ".invalid":          "http://" + host.PrivateAddress + ":5001",
			"gateway-" + ordinal + ".invalid:5100":        host.PrivateAddress + ":5100",
			"http://worker-" + ordinal + ".invalid":       "http://127.0.0.1:1909" + ordinal,
		}
		content = replaceAll(content, replacements)
	}
	if load, ok := findHost(plan.Hosts, "load"); ok {
		content = strings.ReplaceAll(content, "http://host-metrics-load.invalid", "http://"+load.PrivateAddress+":19101")
	}
	return content
}

func replaceAll(content string, replacements map[string]string) string {
	for old, replacement := range replacements {
		content = strings.ReplaceAll(content, old, replacement)
	}
	return content
}
