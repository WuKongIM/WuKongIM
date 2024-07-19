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
	cfgFile             string
	ignoreMissingConfig bool // 配置文件是否可以不存在，如果配置了配置文件，但是不存在，则忽略
	serverOpts          = server.NewOptions()
	mode                string
	daemon              bool
	pidfile             string = "wukongimpid"
	installDir          string
	initialed           bool // 是否已经初始化成功
	rootCmd             = &cobra.Command{
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

	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")

	rootCmd.PersistentFlags().BoolVarP(&ignoreMissingConfig, "ignoreMissingConfig", "i", false, "Whether the configuration file can not exist. If the configuration file is configured but does not exist, it will be ignored")
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "debug", "mode")
	// 后台运行
	rootCmd.PersistentFlags().BoolVarP(&daemon, "daemon", "d", false, "run in daemon mode")

}

func initConfig() {
	vp := viper.New()
	if strings.TrimSpace(cfgFile) != "" {
		vp.SetConfigFile(cfgFile)
		if err := vp.ReadInConfig(); err != nil {
			if !ignoreMissingConfig {
				fmt.Println("read config file error: ", err)
				panic(fmt.Errorf("read config file error: %s", err))
			} else {
				wklog.Error("read config file error", zap.Error(err))
			}
		}
	}

	vp.SetEnvPrefix("wk")
	vp.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	vp.AutomaticEnv()
	// 初始化服务配置
	if strings.TrimSpace(mode) != "" {
		serverOpts.Mode = server.Mode(mode)
	}
	serverOpts.ConfigureWithViper(vp)

	installDir = serverOpts.RootDir

	initialed = true
}

func initServer() {
	if !initialed {
		return
	}
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
		fmt.Println("Root Dir:", serverOpts.RootDir)
		fmt.Println("Config File:", cfgFile)
		fmt.Println("WuKongIM is running in daemon mode")
		err := cmd.Start() // 开始执行新进程，不等待新进程退出
		if err != nil {
			log.Fatal(err)
		}
		err = os.WriteFile(path.Join(serverOpts.RootDir, pidfile), []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
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
