package main

import (
	"github.com/WuKongIM/WuKongIM/internal/app"
	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
)

func loadConfig(args []string) (app.Config, error) {
	return productconfig.Load(productconfig.Options{Args: args})
}
