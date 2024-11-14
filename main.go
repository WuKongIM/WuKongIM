package main

import (
	"embed"

	"github.com/WuKongIM/WuKongIM/cmd"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/version"
	"go.uber.org/automaxprocs/maxprocs"
	"go.uber.org/zap"
)

//go:embed web/dist
var webFS embed.FS

//go:embed demo
var demoFS embed.FS

// go ldflags
var Version string    // version
var Commit string     // git commit id
var CommitDate string // git commit date
var TreeState string  // git tree state

func main() {

	version.Version = Version
	version.Commit = Commit
	version.CommitDate = CommitDate
	version.TreeState = TreeState
	version.WebFs = webFS
	version.DemoFs = demoFS

	// logFile, err := os.OpenFile("./fatal.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0660)
	// if err != nil {
	// 	log.Println("服务启动出错", "打开异常日志文件失败", err)
	// 	return
	// }
	// // 将进程标准出错重定向至文件，进程崩溃时运行时将向该文件记录协程调用栈信息
	// syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))

	undo, err := maxprocs.Set()
	defer undo()
	if err != nil {
		wklog.Warn("maxprocs set error", zap.Error(err))
	}

	cmd.Execute()

}
