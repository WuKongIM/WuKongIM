package cmd

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/server"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

var (
	cfgFile    string
	serverOpts = server.NewOptions()
	mode       string
	imserver   *server.Server
	rootCmd    = &cobra.Command{
		Use:   "wk",
		Short: "WuKongIM, a sleek and high-performance instant messaging platform.",
		Long:  `WuKongIM, a sleek and high-performance instant messaging platform. For more details, please refer to the documentation: https://githubim.com`,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		Run: func(cmd *cobra.Command, args []string) {
			imserver = initServer()
			start(imserver)
		},
	}
)

func init() {

	cobra.OnInitialize(initConfig)

	homeDir, err := server.GetHomeDir()
	if err != nil {
		panic(err)
	}
	defaultConfig := path.Join(homeDir, "wk.yaml")
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", defaultConfig, "config file")
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "debug", "mode")

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
	fmt.Println("ConfigureWithViper...")
	serverOpts.ConfigureWithViper(vp)
}

func initServer() *server.Server {
	logOpts := wklog.NewOptions()
	logOpts.Level = serverOpts.Logger.Level
	logOpts.LogDir = serverOpts.Logger.Dir
	logOpts.LineNum = serverOpts.Logger.LineNum
	wklog.Configure(logOpts)

	s := server.New(serverOpts)

	return s

}

func start(s *server.Server) {
	ss, err := createSystemService(s)
	if err != nil {
		panic(err)
	}
	err = ss.Run()
	if err != nil {
		panic(err)
	}
}

func addCommand(cmd CMD) {
	rootCmd.AddCommand(cmd.CMD())
}

func createSystemService(ser *server.Server) (service.Service, error) {
	svcConfig := &service.Config{
		Name:        "wukongim",
		DisplayName: "wukongim service",
		Description: "Docs at https://githubim.com/",
	}

	ss := newSystemService(ser)
	s, err := service.New(ss, svcConfig)
	if err != nil {
		return nil, fmt.Errorf("service New failed, err: %v\n", err)
	}
	return s, nil
}

func Execute() {
	ctx := &WuKongIMContext{}
	addCommand(newStopCMD(ctx))
	addCommand(newInstallCMD(ctx))
	addCommand(newUninstallCMD(ctx))
	addCommand(newStartCMD(ctx))
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type systemService struct {
	server *server.Server
}

func newSystemService(s *server.Server) *systemService {
	return &systemService{server: s}
}

func (ss *systemService) Start(s service.Service) error {
	err := ss.server.Start()
	if err != nil {
		return err
	}
	return nil
}

func (ss *systemService) Stop(s service.Service) error {
	return ss.server.Stop()
}
