package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"srunkeeper/internal/config"
	"srunkeeper/internal/keeper"
	"srunkeeper/internal/webui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := flag.String("config", filepath.Join(filepath.Dir(exe), "config"), "三个配置文件所在目录（默认可执行文件旁的 config）")
	web := flag.Bool("web", false, "启动本地网页界面并自动重连")
	once := flag.Bool("once", false, "只检查并认证一轮")
	init := flag.Bool("init", false, "初始化缺失的配置文件，不覆盖已有文件")
	ver := flag.Bool("version", false, "显示版本")
	noBrowser := flag.Bool("no-browser", false, "网页模式不自动打开浏览器")
	flag.Parse()
	if *ver {
		fmt.Println("SrunKeeper", version)
		return nil
	}
	if *web && *once {
		return fmt.Errorf("-web 和 -once 不能同时使用")
	}
	if *init {
		if err := webui.InitConfig(*dir); err != nil {
			return err
		}
		fmt.Println("配置已准备：", *dir)
		return nil
	}
	if *web {
		if err := webui.InitConfig(*dir); err != nil {
			return err
		}
	}
	c, err := config.Load(*dir)
	if err != nil {
		return fmt.Errorf("读取配置失败: %w；可先运行 -init 创建配置", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	k := keeper.New(c)
	if *once {
		k.Cycle(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, s := range k.Snapshot().Devices {
			if s.State != "online" {
				return fmt.Errorf("至少一个设备未确认在线")
			}
		}
		return nil
	}
	log.Printf("SrunKeeper %s，设备 %d 个，检测间隔 %d 秒，Ctrl+C 退出", version, len(c.User.Devices), c.System.CheckIntervalSeconds)
	if *web {
		return webui.Run(ctx, k, *dir, !*noBrowser)
	}
	k.Run(ctx)
	return nil
}
