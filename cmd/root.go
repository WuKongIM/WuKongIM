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
	vp         = viper.New()
	id         uint64
	listenAddr string
	join       string
	rootDir    string
	dataDir    string
	portRand   bool // 是否随机端口
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

	rootCmd.PersistentFlags().Uint64Var(&id, "node-id", 0, "node id")
	rootCmd.PersistentFlags().StringVar(&listenAddr, "listen-addr", "", "raft node listen addr")
	rootCmd.PersistentFlags().StringVar(&join, "join", "", "join addr")
	rootCmd.PersistentFlags().StringVar(&rootDir, "root-dir", "", "root dir")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "data dir")
	rootCmd.PersistentFlags().BoolVar(&portRand, "port-rand", false, "port random")
}

func initConfig() {

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
	initFlags()
}

func initFlags() {
	if id != 0 {
		serverOpts.Cluster.NodeID = id
	}

	if portRand {
		serverOpts.Addr = "tcp://0.0.0.0:0"
		serverOpts.WSAddr = "ws://0.0.0.0:0"
		serverOpts.Monitor.Addr = "0.0.0.0:0"
		serverOpts.HTTPAddr = "0.0.0.0:0"
		serverOpts.Cluster.Addr = "tcp://0.0.0.0:0"
		serverOpts.Demo.Addr = "0.0.0.0:0"
	}

	if strings.TrimSpace(listenAddr) != "" {
		if strings.HasPrefix(listenAddr, "tcp://") {
			serverOpts.Cluster.Addr = listenAddr
		} else {
			serverOpts.Cluster.Addr = fmt.Sprintf("tcp://%s", listenAddr)
		}

	}
	if strings.TrimSpace(dataDir) != "" {
		serverOpts.DataDir = dataDir
	}
	if strings.TrimSpace(rootDir) != "" {
		serverOpts.RootDir = rootDir
		serverOpts.ConfigureDataDir()
		serverOpts.ConfigureLog()

	}
	if strings.TrimSpace(join) != "" {
		joinList := make([]string, 0)
		joinStrs := strings.Split(join, ",")
		if len(joinStrs) > 0 {
			for _, v := range joinStrs {
				v = strings.TrimSpace(v)
				if strings.HasPrefix(v, "tcp://") {
					joinList = append(joinList, v)
				} else {
					joinList = append(joinList, fmt.Sprintf("tcp://%s", v))
				}

			}
			serverOpts.Cluster.Join = joinList
		}
	}
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
