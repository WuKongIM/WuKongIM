//go:build e2e

package suite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"
)

// Workspace owns the per-test filesystem layout for e2e child processes.
type Workspace struct {
	// RootDir stores config, data, and stdio artifacts for one test-scoped workspace.
	RootDir string
	// LogRootDir stores node-scoped log directories when logs are redirected outside RootDir.
	LogRootDir string
}

// Suite owns one test-scoped e2e environment.
type Suite struct {
	t          *testing.T
	binaryPath string
	workspace  Workspace
}

// StartedNode describes one started process and its external addresses.
type StartedNode struct {
	Spec    NodeSpec
	Process *NodeProcess
}

// StartedCluster describes one started three-node cluster and its last observations.
type StartedCluster struct {
	Nodes          []StartedNode
	lastReadyz     map[uint64]HTTPObservation
	lastSlotBodies map[uint32]string
}

// Option customizes one e2e cluster start.
type Option interface {
	apply(*suiteOptions)
}

type optionFunc func(*suiteOptions)

func (f optionFunc) apply(options *suiteOptions) {
	f(options)
}

type suiteOptions struct {
	workspaceRootDir    string
	nodeLogRootDir      string
	nodeConfigOverrides map[uint64]map[string]string
}

// WithWorkspaceRootDir stores one test workspace under the provided parent directory.
func WithWorkspaceRootDir(rootDir string) Option {
	return optionFunc(func(options *suiteOptions) {
		options.workspaceRootDir = strings.TrimSpace(rootDir)
	})
}

// WithNodeLogRootDir redirects node log directories under the provided parent directory.
func WithNodeLogRootDir(rootDir string) Option {
	return optionFunc(func(options *suiteOptions) {
		options.nodeLogRootDir = strings.TrimSpace(rootDir)
	})
}

// WithNodeConfigOverrides replaces or appends rendered WK_* config keys for one node.
func WithNodeConfigOverrides(nodeID uint64, overrides map[string]string) Option {
	return optionFunc(func(options *suiteOptions) {
		if len(overrides) == 0 {
			return
		}
		if options.nodeConfigOverrides == nil {
			options.nodeConfigOverrides = make(map[uint64]map[string]string)
		}
		if options.nodeConfigOverrides[nodeID] == nil {
			options.nodeConfigOverrides[nodeID] = make(map[string]string)
		}
		for key, value := range overrides {
			options.nodeConfigOverrides[nodeID][key] = value
		}
	})
}

// NewWorkspace creates a temp workspace with a default node-1 tree for phase 1.
func NewWorkspace(t *testing.T, opts ...Option) Workspace {
	t.Helper()

	options := resolveSuiteOptions(opts...)
	rootDir := allocateWorkspaceRoot(t, options.workspaceRootDir)
	workspace := Workspace{RootDir: rootDir}
	if options.nodeLogRootDir != "" {
		workspace.LogRootDir = filepath.Join(options.nodeLogRootDir, filepath.Base(rootDir))
		require.NoError(t, os.MkdirAll(workspace.LogRootDir, 0o755))
	}
	require.NoError(t, workspace.ensureNodeDirs(1))
	return workspace
}

// New creates a test-scoped suite and resolves the production binary on demand.
func New(t *testing.T) *Suite {
	t.Helper()

	binaryPath, err := resolveBinaryPath()
	require.NoError(t, err)

	return &Suite{
		t:          t,
		binaryPath: binaryPath,
		workspace:  NewWorkspace(t),
	}
}

// StartSingleNodeCluster starts one real child process and waits for WKProto readiness.
func (s *Suite) StartSingleNodeCluster(opts ...Option) *StartedNode {
	s.t.Helper()

	workspace, options := s.startContext(opts...)
	ports := ReserveLoopbackPorts(s.t)
	spec := buildNodeSpec(1, ports, workspace, options)
	require.NoError(s.t, workspace.ensureNodeDirs(spec.ID))
	require.NoError(s.t, os.WriteFile(spec.ConfigPath, []byte(RenderSingleNodeConfig(spec)), 0o644))

	process := &NodeProcess{
		Spec:       spec,
		BinaryPath: s.binaryPath,
	}
	require.NoError(s.t, process.Start())
	s.t.Cleanup(func() {
		require.NoError(s.t, process.Stop())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(s.t, WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	return &StartedNode{Spec: spec, Process: process}
}

// StartThreeNodeCluster starts three real child processes and returns a cluster handle.
func (s *Suite) StartThreeNodeCluster(opts ...Option) *StartedCluster {
	s.t.Helper()

	workspace, options := s.startContext(opts...)
	ports := []PortSet{
		ReserveLoopbackPorts(s.t),
		ReserveLoopbackPorts(s.t),
		ReserveLoopbackPorts(s.t),
	}
	specs := make([]NodeSpec, 0, len(ports))
	for i, portSet := range ports {
		nodeID := uint64(i + 1)
		spec := buildNodeSpec(nodeID, portSet, workspace, options)
		require.NoError(s.t, workspace.ensureNodeDirs(nodeID))
		specs = append(specs, spec)
	}

	for _, spec := range specs {
		require.NoError(s.t, os.WriteFile(spec.ConfigPath, []byte(RenderClusterConfig(spec, specs)), 0o644))
	}

	cluster := &StartedCluster{
		Nodes:          make([]StartedNode, 0, len(specs)),
		lastReadyz:     make(map[uint64]HTTPObservation, len(specs)),
		lastSlotBodies: make(map[uint32]string),
	}
	for _, spec := range specs {
		process := &NodeProcess{Spec: spec, BinaryPath: s.binaryPath}
		require.NoError(s.t, process.Start())
		cluster.Nodes = append(cluster.Nodes, StartedNode{Spec: spec, Process: process})
	}

	s.t.Cleanup(func() {
		for i := len(cluster.Nodes) - 1; i >= 0; i-- {
			if cluster.Nodes[i].Process != nil {
				require.NoError(s.t, cluster.Nodes[i].Process.Stop())
			}
		}
	})

	return cluster
}

// WaitClusterReady waits until every node satisfies the node-ready contract.
func (c *StartedCluster) WaitClusterReady(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("started cluster is nil")
	}
	for _, node := range c.Nodes {
		observation, err := waitNodeReadyDetailed(ctx, node)
		c.lastReadyz[node.Spec.ID] = observation
		if err != nil {
			return fmt.Errorf("node %d not ready: %w", node.Spec.ID, err)
		}
	}
	return nil
}

// DumpDiagnostics returns a cluster-scoped snapshot of the last observations and node artifacts.
func (c *StartedCluster) DumpDiagnostics() string {
	if c == nil {
		return "cluster: <nil>\n"
	}

	var b strings.Builder
	for _, node := range c.Nodes {
		fmt.Fprintf(&b, "node %d diagnostics:\n", node.Spec.ID)
		if observation, ok := c.lastReadyz[node.Spec.ID]; ok {
			fmt.Fprintf(&b, "readyz: status=%d body=%s\n", observation.StatusCode, observation.Body)
		}
		process := node.Process
		if process == nil {
			process = &NodeProcess{Spec: node.Spec}
		}
		b.WriteString(process.DumpDiagnostics())
	}
	for slotID, body := range c.lastSlotBodies {
		fmt.Fprintf(&b, "slot %d body: %s\n", slotID, body)
	}
	return b.String()
}

// Node looks up one node handle by node ID.
func (c *StartedCluster) Node(nodeID uint64) (*StartedNode, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Nodes {
		if c.Nodes[i].Spec.ID == nodeID {
			return &c.Nodes[i], true
		}
	}
	return nil, false
}

// MustNode looks up one node handle by node ID and panics when missing.
func (c *StartedCluster) MustNode(nodeID uint64) *StartedNode {
	node, ok := c.Node(nodeID)
	if !ok {
		panic(fmt.Sprintf("node %d not found", nodeID))
	}
	return node
}

// GatewayAddr returns the public WKProto listen address for the started node.
func (n *StartedNode) GatewayAddr() string {
	return n.Spec.GatewayAddr
}

// Stop terminates the started node process and detaches it from future cleanup.
func (n *StartedNode) Stop() error {
	if n == nil || n.Process == nil {
		return nil
	}
	err := n.Process.Stop()
	if err == nil {
		n.Process = nil
	}
	return err
}

func buildNodeSpec(nodeID uint64, ports PortSet, workspace Workspace, options suiteOptions) NodeSpec {
	return NodeSpec{
		ID:              nodeID,
		Name:            "node-" + strconv.FormatUint(nodeID, 10),
		RootDir:         workspace.NodeRootDir(nodeID),
		DataDir:         workspace.NodeDataDir(nodeID),
		ConfigPath:      workspace.NodeConfigPath(nodeID),
		StdoutPath:      workspace.NodeStdoutPath(nodeID),
		StderrPath:      workspace.NodeStderrPath(nodeID),
		ClusterAddr:     ports.ClusterAddr,
		GatewayAddr:     ports.GatewayAddr,
		APIAddr:         ports.APIAddr,
		ManagerAddr:     ports.ManagerAddr,
		LogDir:          workspace.NodeLogDir(nodeID),
		ConfigOverrides: cloneConfigOverrides(options.nodeConfigOverrides[nodeID]),
	}
}

func (w Workspace) ensureNodeDirs(nodeID uint64) error {
	if err := os.MkdirAll(w.NodeDataDir(nodeID), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(w.NodeLogDir(nodeID), 0o755)
}

// NodeRootDir returns the root directory for one node.
func (w Workspace) NodeRootDir(nodeID uint64) string {
	return filepath.Join(w.RootDir, nodeDirName(nodeID))
}

// NodeDataDir returns the data directory for one node.
func (w Workspace) NodeDataDir(nodeID uint64) string {
	return filepath.Join(w.NodeRootDir(nodeID), "data")
}

// NodeLogDir returns the log directory for one node.
func (w Workspace) NodeLogDir(nodeID uint64) string {
	if w.LogRootDir != "" {
		return filepath.Join(w.LogRootDir, nodeDirName(nodeID))
	}
	return filepath.Join(w.NodeRootDir(nodeID), "logs")
}

// NodeConfigPath returns the config file path for one node.
func (w Workspace) NodeConfigPath(nodeID uint64) string {
	return filepath.Join(w.NodeRootDir(nodeID), "wukongim.conf")
}

// NodeStdoutPath returns the stdout log path for one node.
func (w Workspace) NodeStdoutPath(nodeID uint64) string {
	return filepath.Join(w.NodeRootDir(nodeID), "stdout.log")
}

// NodeStderrPath returns the stderr log path for one node.
func (w Workspace) NodeStderrPath(nodeID uint64) string {
	return filepath.Join(w.NodeRootDir(nodeID), "stderr.log")
}

func nodeDirName(nodeID uint64) string {
	return "node-" + strconv.FormatUint(nodeID, 10)
}

func resolveSuiteOptions(opts ...Option) suiteOptions {
	var options suiteOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.apply(&options)
	}
	return options
}

func (s *Suite) startContext(opts ...Option) (Workspace, suiteOptions) {
	options := resolveSuiteOptions(opts...)
	if options.workspaceRootDir == "" && options.nodeLogRootDir == "" {
		return s.workspace, options
	}
	return NewWorkspace(s.t, opts...), options
}

func allocateWorkspaceRoot(t *testing.T, parent string) string {
	t.Helper()

	parent = strings.TrimSpace(parent)
	if parent == "" {
		return t.TempDir()
	}
	require.NoError(t, os.MkdirAll(parent, 0o755))
	rootDir, err := os.MkdirTemp(parent, sanitizeTestName(t.Name())+"-")
	require.NoError(t, err)
	return rootDir
}

func sanitizeTestName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "e2e"
	}

	var b strings.Builder
	b.Grow(len(name))
	lastDash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if lastDash {
			continue
		}
		b.WriteByte('-')
		lastDash = true
	}

	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return "e2e"
	}
	return sanitized
}

func cloneConfigOverrides(overrides map[string]string) map[string]string {
	if len(overrides) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(overrides))
	for key, value := range overrides {
		cloned[key] = value
	}
	return cloned
}
