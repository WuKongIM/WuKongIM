package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/server"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/judwhite/go-svc"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	cfgFile    string
	serverOpts = server.NewOptions()
	mode       string
	daemon     bool
	pidfile    string = "WukongimPID"
	installDir string
	rootCmd    = &cobra.Command{
		Use:   "wk",
		Short: "WuKongIM, a sleek and high-performance instant messaging platform.",
		Long:  `WuKongIM, a sleek and high-performance instant messaging platform. For more details, please refer to the documentation: https://githubim.com`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		Run: func(cmd *cobra.Command, args []string) {
			initServer()
		},
	}
)

func init() {

	homeDir, err := server.GetHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	installDir = homeDir

	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "debug", "mode")
	// 后台运行
	rootCmd.PersistentFlags().BoolVarP(&daemon, "daemon", "d", false, "run in daemon mode")

}

func initConfig() {
	vp := viper.New()
	if cfgFile != "" {
		vp.SetConfigFile(cfgFile)
		if err := vp.ReadInConfig(); err != nil {
			fmt.Println("Using config file:", vp.ConfigFileUsed(), zap.Error(err))
		}
	}

	vp.SetEnvPrefix("wk")
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	vp.AutomaticEnv()
	// 初始化服务配置
	serverOpts.ConfigureWithViper(vp)
}

func initServer() {
	logOpts := wklog.NewOptions()
	logOpts.Level = serverOpts.Logger.Level
	logOpts.LogDir = serverOpts.Logger.Dir
	logOpts.LineNum = serverOpts.Logger.LineNum
	wklog.Configure(logOpts)

	s := server.New(serverOpts)
	if daemon {
		filePath, _ := filepath.Abs(os.Args[0])
		args := os.Args[1:]
		newArgs := make([]string, 0)
		for _, arg := range args {
			if arg == "-d" || arg == "--daemon" {
				continue
			}
			newArgs = append(newArgs, arg)
		}
		cmd := exec.Command(filePath, newArgs...)
		// 将其他命令传入生成出的进程
		// cmd.Stdin = os.Stdin // 给新进程设置文件描述符，可以重定向到文件中
		// cmd.Stdout = os.Stdout
		// cmd.Stderr = os.Stderr
		err := cmd.Start() // 开始执行新进程，不等待新进程退出
		if err != nil {
			log.Fatal(err)
		}
		err = os.WriteFile(path.Join(installDir, pidfile), []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else {
		if err := svc.Run(s); err != nil {
			log.Fatal(err)
		}
	}

}
func addCommand(cmd CMD) {
	rootCmd.AddCommand(cmd.CMD())
}

func Execute() {
	ctx := &WuKongIMContext{}
	addCommand(newStopCMD(ctx))
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
