//go:build !genonly

package main

import (
	"flag"
	"fmt"

	"github.com/ramoncjs3/fries/internal/config"
)

// runDSN 打印数据库连接串，给 Makefile 里的 goose 用。
//
// 好处是「配置只有一处」：改 config.yaml 就够了，Makefile 不用再抄一份数据库参数。
func runDSN(_ string, args []string) error {
	fs := flag.NewFlagSet("dsn", flag.ContinueOnError)
	path := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	fmt.Println(cfg.DSN())
	return nil
}

func init() {
	extraCommands = append(extraCommands,
		command{"dsn", "打印数据库连接串", runDSN})
}
