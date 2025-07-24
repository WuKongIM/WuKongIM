package cmd

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/server"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.uber.org/zap"
)

var (
	cfgFile             string
	ignoreMissingConfig bool // 配置文件是否可以不存在，如果配置了配置文件，但是不存在，则忽略
	serverOpts          = options.New()
	mode                string
	daemon              bool
	pidfile             string = "wukongim.pid"
	pingback            string // pingback地址
	noStdout            bool
	installDir          string
	initialed           bool // 是否已经初始化成功
	rootCmd             = &cobra.Command{
		Use:   "wk",
		Short: "WuKongIM, a sleek and high-performance instant messaging platform.",
		Long:  `WuKongIM, a sleek and high-performance instant messaging platform. For more details, please refer to the documentation: https://githubim.com`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmdRun()
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
	rootCmd.PersistentFlags().StringVar(&pingback, "pingback", "", "pingback address")
	rootCmd.PersistentFlags().BoolVarP(&noStdout, "noStdout", "", false, "no stdout")

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
		serverOpts.Mode = options.Mode(mode)
	}
	serverOpts.ConfigureWithViper(vp)

	installDir = serverOpts.RootDir

	initialed = true
}

func cmdRun() error {
	if !initialed {
		return nil
	}
	logOpts := wklog.NewOptions()
	logOpts.Level = serverOpts.Logger.Level
	logOpts.LogDir = serverOpts.Logger.Dir
	logOpts.LineNum = serverOpts.Logger.LineNum
	logOpts.NodeId = serverOpts.Cluster.NodeId
	logOpts.TraceOn = serverOpts.Logger.TraceOn
	logOpts.NoStdout = noStdout
	wklog.Configure(logOpts)

	if daemon { // 后台运行
		// 以子进程方式启动
		fmt.Println("start as child process")
		startAsChildProcess()
	} else {
		s := server.New(serverOpts)
		err := s.Start()
		if err != nil {
			wklog.Error("start server error", zap.Error(err))
			return err
		}
		// 等待集群准备好
		s.MustWaitAllSlotsReady(time.Minute)

		// 处理 pingback (如果提供了)
		if pingback != "" {
			if err := handlePingback(); err != nil {
				s.Stop() // 如果 pingback 失败，也尝试停止服务器
				return err
			}
			if err := writePIDFile(); err != nil {
				s.Stop() // 如果写 PID 文件失败，也尝试停止服务器
				return err
			}
		}

		// 设置信号监听，等待退出信号
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		wklog.Info("WuKongIM server started successfully. Press Ctrl+C to exit.")

		// 阻塞直到收到退出信号
		<-quit

		s.Stop() // <-- 调用 Stop() 进行清理
		wklog.Info("WuKongIM server stopped.")
	}
	return nil
}

// 将 pingback 处理逻辑提取到一个单独的函数中
func handlePingback() error {
	confirmationBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		wklog.Error("read confirmation error from stdin", zap.Error(err))
		return err
	}
	conn, err := net.Dial("tcp", pingback)
	if err != nil {
		wklog.Error("dialing confirmation address", zap.Error(err))
		return err
	}
	defer conn.Close()

	_, err = conn.Write(confirmationBytes)
	if err != nil {
		wklog.Error("write confirmation error", zap.Error(err))
		return err
	}
	return nil
}

// 将写 PID 文件逻辑提取到一个单独的函数中
func writePIDFile() error {
	err := os.WriteFile(path.Join(".", pidfile), []byte(strconv.Itoa(os.Getpid())), 0o600)
	if err != nil {
		wklog.Error("write pid file error", zap.Error(err))
		return err
	}
	return nil
}

func newRunCmd(listener net.Listener) (*exec.Cmd, error) {
	filePath, _ := filepath.Abs(os.Args[0])
	args := os.Args[1:]
	newArgs := make([]string, 0)
	if listener != nil {
		newArgs = append(newArgs, "--pingback", listener.Addr().String())
	}
	for _, arg := range args {
		if arg == "-d" || arg == "--daemon" {
			continue
		}
		newArgs = append(newArgs, arg)
	}

	if !wkutil.ArrayContains(newArgs, "--noStdout") {
		newArgs = append(newArgs, "--noStdout")
	}

	cmd := exec.Command(filePath, newArgs...)

	// 允许相对路径运行
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}

func startAsChildProcess() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		wklog.Panic("listen error", zap.Error(err))
	}
	defer ln.Close()

	cmd, err := newRunCmd(ln)
	if err != nil {
		wklog.Panic("new cmd failed", zap.Error(err))
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		wklog.Panic("get stdin pipe failed", zap.Error(err))
	}

	// 生成一个随机的32字节的数据，用于验证子进程是否启动成功
	expect := make([]byte, 32)
	_, err = rand.Read(expect)
	if err != nil {
		wklog.Panic("rand read error", zap.Error(err))
	}

	go func() {
		_, _ = stdinPipe.Write(expect)
		stdinPipe.Close()
	}()

	// start the process
	err = cmd.Start()
	if err != nil {
		wklog.Panic("starting WuKongIM process failed", zap.Error(err))
	}

	success, exit := make(chan struct{}), make(chan error)

	// 开启一个goroutine监听子进程是否启动成功
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println(err)
				}
				break
			}
			err = handlePingbackConn(conn, expect)
			if err == nil {
				close(success)
				break
			}
			log.Println(err)
		}
	}()

	go func() {
		err := cmd.Wait()
		exit <- err
	}()

	select {
	case <-success:
		wklog.Info("Successfully started WuKongIM - is running in the background", zap.Int("pid", cmd.Process.Pid))
	case err := <-exit:
		wklog.Error("WuKongIM process exited with error", zap.Error(err))
	}
}

func handlePingbackConn(conn net.Conn, expect []byte) error {
	defer conn.Close()
	confirmationBytes, err := io.ReadAll(io.LimitReader(conn, 32))
	if err != nil {
		return err
	}
	if !bytes.Equal(confirmationBytes, expect) {
		return fmt.Errorf("wrong confirmation: %x", confirmationBytes)
	}
	return nil
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
