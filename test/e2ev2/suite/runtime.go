//go:build e2e

package suite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"
)

// Workspace owns the per-test filesystem layout for e2ev2 child processes.
type Workspace struct {
	// RootDir stores config, data, and stdio artifacts for one test-scoped workspace.
	RootDir string
	// LogRootDir stores node-scoped log directories when logs are redirected outside RootDir.
	LogRootDir string
}

// Suite owns one test-scoped e2ev2 environment.
type Suite struct {
	t          *testing.T
	binaryPath string
	workspace  Workspace
}

// StartedNode describes one started wukongimv2 process and its external addresses.
type StartedNode struct {
	Spec    NodeSpec
	Process *NodeProcess
}

// StartedCluster describes one started static v2 cluster and its observations.
type StartedCluster struct {
	Nodes      []StartedNode
	lastReadyz map[uint64]HTTPObservation
	binaryPath string
	workspace  Workspace
	options    suiteOptions
}

// Option customizes one e2ev2 cluster start.
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
	managerHTTP         bool
	dynamicJoinToken    string
	nodeConfigOverrides map[uint64]map[string]string
	nodeEnv             map[uint64][]string
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

// WithManagerHTTP enables the public manager HTTP listener for started nodes.
func WithManagerHTTP() Option {
	return optionFunc(func(options *suiteOptions) {
		options.managerHTTP = true
	})
}

// WithDynamicJoinToken configures static nodes to accept seed JoinNode RPCs.
func WithDynamicJoinToken(token string) Option {
	return optionFunc(func(options *suiteOptions) {
		options.dynamicJoinToken = strings.TrimSpace(token)
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

// WithNodeEnv appends process environment variables for one started node.
func WithNodeEnv(nodeID uint64, env ...string) Option {
	return optionFunc(func(options *suiteOptions) {
		if len(env) == 0 {
			return
		}
		if options.nodeEnv == nil {
			options.nodeEnv = make(map[uint64][]string)
		}
		options.nodeEnv[nodeID] = append(options.nodeEnv[nodeID], env...)
	})
}

// NewWorkspace creates a temp workspace with a default node-1 tree.
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

// New creates a test-scoped suite and resolves the wukongimv2 binary on demand.
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

// StartSingleNodeCluster starts one real wukongimv2 child process and waits for public readiness.
func (s *Suite) StartSingleNodeCluster(opts ...Option) *StartedNode {
	s.t.Helper()

	workspace, options := s.startContext(opts...)
	ports := ReserveLoopbackPorts(s.t)
	spec := buildNodeSpec(1, ports, workspace, options)
	require.NoError(s.t, workspace.ensureNodeDirs(spec.ID))
	renderedConfig := RenderSingleNodeConfig(spec)
	require.NoError(s.t, os.WriteFile(spec.ConfigPath, []byte(renderedConfig), 0o644))
	spec.Env = append(envFromConfig(renderedConfig), spec.Env...)

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
	require.NoError(s.t, WaitHTTPReady(ctx, spec.APIAddr, "/readyz"), process.DumpDiagnostics())
	require.NoError(s.t, WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	return &StartedNode{Spec: spec, Process: process}
}

// StartThreeNodeCluster starts three static wukongimv2 child processes.
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
		if options.dynamicJoinToken != "" {
			setSpecConfigOverride(&spec, "WK_CLUSTER_JOIN_TOKEN", options.dynamicJoinToken)
		}
		require.NoError(s.t, workspace.ensureNodeDirs(nodeID))
		specs = append(specs, spec)
	}

	for i := range specs {
		renderedConfig := RenderClusterConfig(specs[i], specs)
		require.NoError(s.t, os.WriteFile(specs[i].ConfigPath, []byte(renderedConfig), 0o644))
		specs[i].Env = append(envFromConfig(renderedConfig), specs[i].Env...)
	}

	cluster := &StartedCluster{
		Nodes:      make([]StartedNode, 0, len(specs)),
		lastReadyz: make(map[uint64]HTTPObservation, len(specs)),
		binaryPath: s.binaryPath,
		workspace:  workspace,
		options:    options,
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

// SeedJoinNodeConfig describes one dynamic data-node seed-join process.
type SeedJoinNodeConfig struct {
	// NodeID is the stable non-zero identity requested by the joining node.
	NodeID uint64
	// Seeds contains reachable existing cluster RPC addresses.
	Seeds []string
	// JoinAddr is the stable cluster RPC address advertised into membership.
	JoinAddr string
	// JoinToken authenticates the pre-membership JoinNode RPC.
	JoinToken string
	// ClusterID is the expected ControllerV2 cluster identity.
	ClusterID string
}

// SeedAddrs returns stable seed RPC addresses from the currently started cluster nodes.
func (c *StartedCluster) SeedAddrs() []string {
	if c == nil {
		return nil
	}
	nodes := append([]StartedNode(nil), c.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Spec.ID < nodes[j].Spec.ID
	})
	addrs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Spec.ClusterAddr != "" {
			addrs = append(addrs, node.Spec.ClusterAddr)
		}
	}
	return addrs
}

// NodeAddr returns the cluster RPC address for an already started node.
func (c *StartedCluster) NodeAddr(nodeID uint64) string {
	node := c.MustNode(nodeID)
	return node.Spec.ClusterAddr
}

// StartSeedJoinNode starts one dynamic data node that joins through configured seeds.
func (c *StartedCluster) StartSeedJoinNode(t testing.TB, cfg SeedJoinNodeConfig) *StartedNode {
	t.Helper()

	started := c.StartSeedJoinNodeNoWait(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	observation, err := waitHTTPReadyDetailed(ctx, started.Spec.APIAddr, "/readyz")
	c.lastReadyz[started.Spec.ID] = observation
	require.NoError(t, err, c.DumpDiagnostics())

	return started
}

// StartSeedJoinNodeNoWait starts one dynamic data node without waiting for readiness.
func (c *StartedCluster) StartSeedJoinNodeNoWait(t testing.TB, cfg SeedJoinNodeConfig) *StartedNode {
	t.Helper()
	require.NotNil(t, c, "started cluster is nil")
	require.NotEmpty(t, c.binaryPath, "started cluster binary path is empty")
	require.NotZero(t, cfg.NodeID, "seed join node id must be non-zero")

	if len(cfg.Seeds) == 0 {
		cfg.Seeds = c.SeedAddrs()
	}
	if strings.TrimSpace(cfg.JoinToken) == "" {
		cfg.JoinToken = c.options.dynamicJoinToken
	}
	if strings.TrimSpace(cfg.ClusterID) == "" {
		cfg.ClusterID = staticThreeNodeClusterID
	}
	require.NotEmpty(t, cfg.Seeds, "seed join seeds must not be empty")
	require.NotEmpty(t, strings.TrimSpace(cfg.JoinToken), "seed join token must not be empty")

	ports := ReserveLoopbackPorts(t)
	spec := buildNodeSpec(cfg.NodeID, ports, c.workspace, c.options)
	if strings.TrimSpace(cfg.JoinAddr) == "" {
		cfg.JoinAddr = spec.ClusterAddr
	}

	require.NoError(t, c.workspace.ensureNodeDirs(spec.ID))
	renderedConfig := RenderSeedJoinNodeConfig(spec, cfg)
	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderedConfig), 0o644))
	spec.Env = append(envFromConfig(renderedConfig), spec.Env...)

	process := &NodeProcess{Spec: spec, BinaryPath: c.binaryPath}
	require.NoError(t, process.Start())
	c.Nodes = append(c.Nodes, StartedNode{Spec: spec, Process: process})
	if c.lastReadyz == nil {
		c.lastReadyz = make(map[uint64]HTTPObservation)
	}
	return &c.Nodes[len(c.Nodes)-1]
}

// WaitHTTPReady waits until every cluster node satisfies the public HTTP readiness contract.
func (c *StartedCluster) WaitHTTPReady(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("started cluster is nil")
	}
	if c.lastReadyz == nil {
		c.lastReadyz = make(map[uint64]HTTPObservation, len(c.Nodes))
	}
	for _, node := range c.Nodes {
		observation, err := waitHTTPReadyDetailed(ctx, node.Spec.APIAddr, "/readyz")
		c.lastReadyz[node.Spec.ID] = observation
		if err != nil {
			return fmt.Errorf("node %d http not ready: %w", node.Spec.ID, err)
		}
	}
	return nil
}

// WaitClusterReady waits until every node satisfies the public v2 readiness contract.
func (c *StartedCluster) WaitClusterReady(ctx context.Context) error {
	if err := c.WaitHTTPReady(ctx); err != nil {
		return err
	}
	for _, node := range c.Nodes {
		if err := WaitWKProtoReady(ctx, node.Spec.GatewayAddr); err != nil {
			return fmt.Errorf("node %d wkproto not ready: %w", node.Spec.ID, err)
		}
	}
	return nil
}

// DumpDiagnostics returns a cluster-scoped snapshot of readiness and node artifacts.
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
		b.WriteString(node.DumpDiagnostics())
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

// APIAddr returns the public HTTP API listen address for the started node.
func (n StartedNode) APIAddr() string {
	return n.Spec.APIAddr
}

// ManagerAddr returns the public manager HTTP listen address for the started node.
func (n StartedNode) ManagerAddr() string {
	return n.Spec.ManagerAddr
}

// GatewayAddr returns the public WKProto listen address for the started node.
func (n StartedNode) GatewayAddr() string {
	return n.Spec.GatewayAddr
}

// DumpDiagnostics returns diagnostics for the started node process.
func (n StartedNode) DumpDiagnostics() string {
	if n.Process != nil {
		return n.Process.DumpDiagnostics()
	}
	return (&NodeProcess{Spec: n.Spec}).DumpDiagnostics()
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
	managerAddr := ""
	if options.managerHTTP {
		managerAddr = ports.ManagerAddr
	}
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
		ManagerAddr:     managerAddr,
		LogDir:          workspace.NodeLogDir(nodeID),
		ConfigOverrides: cloneConfigOverrides(options.nodeConfigOverrides[nodeID]),
		Env:             cloneEnv(options.nodeEnv[nodeID]),
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
	return "node-" + nodeIDString(nodeID)
}

func nodeIDString(nodeID uint64) string {
	return strconv.FormatUint(nodeID, 10)
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
		return "e2ev2"
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
		return "e2ev2"
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

func cloneEnv(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	return append([]string(nil), env...)
}

func setSpecConfigOverride(spec *NodeSpec, key, value string) {
	if spec.ConfigOverrides == nil {
		spec.ConfigOverrides = make(map[string]string)
	}
	spec.ConfigOverrides[key] = value
}
