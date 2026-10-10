package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NguyenPhat-NT/chia_se_web/internal/app"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "GửiWeb:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("dùng: guiweb serve|admin|reset-password|version")
	}
	command := os.Args[1]
	if command == "version" {
		fmt.Println("GửiWeb", version)
		return nil
	}
	if command != "serve" && command != "admin" && command != "reset-password" {
		return errors.New("lệnh không hợp lệ")
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	configPath := f.String("config", "", "đường dẫn cấu hình JSON")
	dev := f.Bool("dev", false, "chạy thử bằng HTTP, chỉ trên loopback")
	dataDir := f.String("data-dir", "/tmp/guiweb-dev", "thư mục dữ liệu khi dùng -dev")
	listen := f.String("listen", "127.0.0.1:8088", "listener khi dùng -dev")
	username := f.String("username", "", "tên tài khoản cho admin/reset-password")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("tham số thừa")
	}
	var cfg app.Config
	var err error
	if *dev {
		if *configPath != "" {
			return errors.New("chỉ chọn -dev hoặc -config")
		}
		cfg = app.DevConfig(*dataDir)
		cfg.Listen = *listen
	} else {
		if *configPath == "" {
			return errors.New("production cần -config; chạy thử dùng -dev")
		}
		cfg, err = app.LoadConfig(*configPath)
		if err != nil {
			return err
		}
	}
	s, err := app.New(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	if command != "serve" {
		if *username == "" {
			return errors.New("cần -username")
		}
		pass, err := readPassword()
		if err != nil {
			return err
		}
		if command == "admin" {
			err = s.CreateAdmin(*username, pass)
		} else {
			err = s.ResetPassword(*username, pass)
		}
		if err != nil {
			return err
		}
		fmt.Println("Đã lưu tài khoản.")
		return nil
	}
	server := &http.Server{Addr: cfg.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Minute, WriteTimeout: 10 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	fmt.Fprintf(os.Stderr, "GửiWeb %s đang lắng nghe %s (quản lý: %s)\n", version, cfg.Listen, cfg.AdminHost)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
		return nil
	}
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("nhập mật khẩu từ terminal; không dùng đối số hoặc biến môi trường")
	}
	fmt.Fprint(os.Stderr, "Mật khẩu (12–72 byte): ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Nhập lại: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("hai mật khẩu không trùng nhau")
	}
	return string(first), nil
}
