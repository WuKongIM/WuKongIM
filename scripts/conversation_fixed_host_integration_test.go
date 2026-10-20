//go:build integration

package scripts_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A timed-out driver can leave a node in a different process group; cleanup
// must cover the entire new session without touching the caller's session.
func TestConversationFixedHostTimeoutCleansChildProcessGroups(t *testing.T) {
	root := repoRoot(t)
	code := `import importlib.util,json,os,pathlib,sys,tempfile
spec=importlib.util.spec_from_file_location("fixed",sys.argv[1]); mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
with tempfile.TemporaryDirectory() as d:
 p=pathlib.Path(d); child=p/"child.pid"
 command=[sys.executable,"-c","import os,pathlib,subprocess,sys,time; c=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'],process_group=0); pathlib.Path(sys.argv[1]).write_text(str(c.pid)); time.sleep(60)",str(child)]
 result=mod.run_command(command,pathlib.Path.cwd(),dict(os.environ),p/"test.log",0.8)
 assert result["timed_out"], result
 assert result["cleanup_complete"], result
 pid=int(child.read_text())
 stat=pathlib.Path('/proc')/str(pid)/'stat'
 assert not stat.exists() or stat.read_text().split(') ')[1].split()[0]=='Z'
 assert os.getsid(0)!=result["session_id"]
 print(json.dumps(result))
`
	if out, err := exec.Command("uname", "-s").Output(); err != nil || string(out) != "Linux\n" {
		t.Skip("Linux process session cleanup")
	}
	out, err := exec.Command("python3", "-B", "-c", code, filepath.Join(root, "scripts", "run-conversation-fixed-host.py")).CombinedOutput()
	require.NoError(t, err, "%s", out)
}
