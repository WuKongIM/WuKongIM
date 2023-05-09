package main

import (
	"log"
	"os"
	"syscall"

	"github.com/WuKongIM/WuKongIM/cmd"
)

func main() {

	logFile, err := os.OpenFile("./fatal.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0660)
	if err != nil {
		log.Println("服务启动出错", "打开异常日志文件失败", err)
		return
	}
	// 将进程标准出错重定向至文件，进程崩溃时运行时将向该文件记录协程调用栈信息
	syscall.Dup2(int(logFile.Fd()), int(os.Stderr.Fd()))

	cmd.Execute()
}
