package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/WuKongIM/WuKongIM/internal/server"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/judwhite/go-svc"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// go ldflags
var Version string
var Commit string
var CommitDate string

var (
	cfgFile    string
	serverOpts = server.NewOptions()
	mode       string
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
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.wk.yaml)")
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "debug", "mode")

}

func initConfig() {
	vp := viper.New()
	if cfgFile != "" {
		vp.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		vp.AddConfigPath(home)
		vp.SetConfigType("yaml")
		vp.SetConfigName(".wk")
	}

	if err := vp.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
	vp.BindPFlags(rootCmd.Flags())
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

	if err := svc.Run(s); err != nil {
		log.Fatal(err)
	}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
