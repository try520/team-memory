package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"team-memory/internal/config"
	"team-memory/internal/kb"
	"team-memory/internal/mcpserver"
	"team-memory/internal/store"
)

func main() {
	cfg := config.Load()
	logger := log.New(os.Stdout, "memory ", log.LstdFlags|log.Lmsgprefix)

	st := store.New(cfg.DataDir)
	svc := kb.New(st)
	if err := svc.EnsureServiceLayout(); err != nil {
		logger.Fatalf("初始化数据目录失败: %v", err)
	}
	release, err := acquirePID(cfg.DataDir)
	if err != nil {
		logger.Fatalf("%v", err)
	}
	defer release()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mcpserver.NewHandler(svc, st, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Printf("team-memory %s 启动，监听 %s，数据目录 %s，项目 %d 个",
		mcpserver.Version, cfg.Addr, cfg.DataDir, len(svc.ListProjects()))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("HTTP 服务退出: %v", err)
	}
	logger.Print("已停止")
}

// acquirePID 保证同一数据目录只跑一个实例（最重要的一条并发保护）。
func acquirePID(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	pidPath := filepath.Join(dataDir, ".pid")
	if b, err := os.ReadFile(pidPath); err == nil {
		if pid, cerr := strconv.Atoi(strings.TrimSpace(string(b))); cerr == nil && pid != os.Getpid() && processAlive(pid) {
			return nil, fmt.Errorf("数据目录 %s 已被进程 %d 占用：本服务只允许单实例", dataDir, pid)
		}
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		// 只读挂载或受限目录：单实例保护写不进去就没法保证安全，直接退出但把原因说清楚。
		return nil, fmt.Errorf("写 PID 文件 %s 失败（数据目录需要可写）: %w", pidPath, err)
	}
	return func() { _ = os.Remove(pidPath) }, nil
}

func processAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		// Windows 下 signal(0) 不可用；容器化部署实际跑在 Linux。
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
