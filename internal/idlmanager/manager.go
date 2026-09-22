package idlmanager

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/transport"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	etcd "github.com/kitex-contrib/registry-etcd"
	"github.com/kouleen/gateway/internal/config"
	gen "github.com/kouleen/gateway/internal/generic"
	"github.com/kouleen/gateway/internal/middleware"
	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

// Manager IDL仓库管理器
type Manager struct {
	cfg     *config.Config
	gitRepo *git.Repository
	mu      sync.Mutex
}

var (
	manager     *Manager
	once        sync.Once
	updateGroup singleflight.Group
)

// InitManager 初始化单例
func InitManager(cfg *config.Config) *Manager {
	once.Do(func() {
		gen.InitPool()
		manager = &Manager{cfg: cfg}

		// 初始化本地仓库
		if err := manager.initRepo(); err != nil {
			log.Fatalf("init IDL repo failed: %v", err)
		}

		// 首次加载所有客户端和路由
		if err := manager.reloadAll(); err != nil {
			log.Fatalf("first load failed: %v", err)
		}

		logger.Infof("IDL manager initialized successfully")
	})
	return manager
}

// 返回nil代表公开仓库不需要认证
func (m *Manager) getGitAuth() *http.BasicAuth {
	if m.cfg.GitAuthPassword == "" {
		return nil
	}
	return &http.BasicAuth{
		Username: m.cfg.GitAuthUser,
		Password: m.cfg.GitAuthPassword,
	}
}

// initRepo 初始化仓库入口：存在则更新，不存在则克隆；仓库损坏时自动重建
func (m *Manager) initRepo() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := m.cfg.IDLLocalPath
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Infof("local IDL repo not found, shallow clone start")
			return m.cloneRaw()
		}
		return fmt.Errorf("stat repo path failed: %w", err)
	}
	// 路径存在但不是文件夹，异常，强制重建
	if !stat.IsDir() {
		logger.Warnf("repo path is not directory, cleanup and re-clone")
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		m.gitRepo = nil
		return m.cloneRaw()
	}

	// 目录存在，打开仓库
	repo, err := git.PlainOpen(path)
	if err != nil {
		logger.Warnf("open local repo failed, cleanup and re-clone, err=%v", err)
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		m.gitRepo = nil
		return m.cloneRaw()
	}
	m.gitRepo = repo

	// 执行 fetch + hard reset 替代 pull
	return m.fetchAndResetRaw()
}

// cloneRaw 内部裸克隆，不加锁（上层initRepo已经加锁）
func (m *Manager) cloneRaw() error {
	repo, err := git.PlainClone(m.cfg.IDLLocalPath, false, &git.CloneOptions{
		URL:           m.cfg.IDLRepoURL,
		ReferenceName: plumbing.NewBranchReferenceName(m.cfg.IDLRepoBranch),
		Depth:         1, // 浅克隆，仅拉最新commit
		Auth:          m.getGitAuth(),
		Progress:      os.Stdout,
	})
	if err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	m.gitRepo = repo
	logger.Infof("shallow clone success")
	return nil
}

// fetchAndResetRaw 裸更新逻辑，上层已加锁；兼容浅仓库，不再使用Pull
func (m *Manager) fetchAndResetRaw() error {
	repo := m.gitRepo
	remote, err := repo.Remote("origin")
	if err != nil {
		return fmt.Errorf("get remote origin failed: %w", err)
	}

	// 浅克隆必须设置 Depth:1，否则fetch拿不到新提交
	fetchOpts := &git.FetchOptions{
		Depth:    1,
		Auth:     m.getGitAuth(),
		Progress: os.Stdout,
	}
	err = remote.Fetch(fetchOpts)
	if err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			logger.Infof("IDL repo already up‑to‑date")
			return nil
		}
		errMsg := err.Error()
		// 对象缺失，触发强制重建
		if strings.Contains(errMsg, "object not found") {
			logger.Warnf("git object missing, force re-clone")
			if err := os.RemoveAll(m.cfg.IDLLocalPath); err != nil {
				return err
			}
			m.gitRepo = nil
			return m.cloneRaw()
		}
		return fmt.Errorf("fetch remote failed: %w", err)
	}

	// 获取远程分支引用 origin/xxx
	remoteRefName := plumbing.NewRemoteReferenceName("origin", m.cfg.IDLRepoBranch)
	remoteRef, err := repo.Reference(remoteRefName, true)
	if err != nil {
		return fmt.Errorf("get remote ref %s failed: %w", remoteRefName, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree failed: %w", err)
	}

	// HardReset：无条件覆盖本地，对齐远程HEAD，不会出现non‑fast-forward
	err = wt.Reset(&git.ResetOptions{
		Commit: remoteRef.Hash(),
		Mode:   git.HardReset,
	})
	if err != nil {
		return fmt.Errorf("hard reset to %s failed: %w", remoteRef.Hash().String(), err)
	}
	logger.Infof("fetch & hard reset success, latest commit=%s", remoteRef.Hash().String())
	return nil
}

// PullRepo 对外暴露的更新方法（定时轮询调用）
func (m *Manager) PullRepo() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.gitRepo == nil {
		return m.initRepo()
	}
	return m.fetchAndResetRaw()
}

// ForceReClone 强制清理并重新克隆（故障恢复用）
func (m *Manager) ForceReClone() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	logger.Warnf("force re-clone IDL repo")
	if err := os.RemoveAll(m.cfg.IDLLocalPath); err != nil {
		return err
	}
	m.gitRepo = nil
	return m.cloneRaw()
}

// reloadAll 重新加载所有客户端和路由
func (m *Manager) reloadAll() error {
	// 1. 读取配置文件
	servicesPath := filepath.Join(m.cfg.IDLLocalPath, "/gateway/services.yaml")
	servicesData, err := os.ReadFile(servicesPath)
	if err != nil {
		return fmt.Errorf("read services.yaml failed: %w", err)
	}

	var svcCfg config.ServersConfig
	if err = yaml.Unmarshal(servicesData, &svcCfg); err != nil {
		return fmt.Errorf("parse services.yaml failed: %w", err)
	}

	whitePath := filepath.Join(m.cfg.IDLLocalPath, "/gateway/white-path.yaml")
	whiteData, err := os.ReadFile(whitePath)
	if err != nil {
		return fmt.Errorf("read white-path.yaml failed: %w", err)
	}

	var pathCfg config.WhitePathConfig
	if err := yaml.Unmarshal(whiteData, &pathCfg); err != nil {
		return fmt.Errorf("parse white-path.yaml failed: %w", err)
	}

	middleware.ReplaceWhitelistPaths(pathCfg.WhitePaths)

	// 2. 复用etcd解析器
	resolver, err := etcd.NewEtcdResolver(m.cfg.EtcdEndpoints)
	if err != nil {
		return fmt.Errorf("init etcd resolver failed: %w", err)
	}

	// 3. 批量构建新客户端
	newPool := &sync.Map{}
	newRouteTable := make(map[string]config.RouteTarget)

	for key, svc := range svcCfg.Servers {
		idlFullPath := filepath.Join(m.cfg.IDLLocalPath, svc.IDLPath)

		// 加载IDL
		provider, err := generic.NewThriftFileProvider(idlFullPath)
		if err != nil {
			return fmt.Errorf("load IDL [%s] failed: %w", key, err)
		}

		// Map泛化器，支持map入参
		thriftGeneric, err := generic.MapThriftGeneric(provider)
		if err != nil {
			return fmt.Errorf("create generic [%s] failed: %w", key, err)
		}

		// 构建泛化客户端
		cli, err := genericclient.NewClient(
			svc.ServiceName,
			thriftGeneric,
			client.WithResolver(resolver),
			client.WithTransportProtocol(transport.TTHeaderFramed),
			client.WithMetaHandler(transmeta.ClientTTHeaderHandler),
			client.WithRPCTimeout(10*time.Second),
		)
		if err != nil {
			return fmt.Errorf("build client [%s] failed: %w", key, err)
		}

		newPool.Store(key, cli)

		// 构建路由映射  POST:/api/user/login
		for _, route := range svc.Routes {
			newRouteTable[route.HTTPMethod+":"+route.Path] = config.RouteTarget{
				ServiceKey: key,
				RPCMethod:  route.Method,
			}
		}

		logger.Infof("service [%s] loaded, routes: %d", key, len(svc.Routes))
	}

	// 4. 全部成功后原子替换
	gen.ReplacePool(newPool)
	config.ReplaceRouteTable(newRouteTable)

	logger.Infof("reload completed, total services: %d, total routes: %d", len(svcCfg.Servers), len(newRouteTable))
	return nil
}

// TriggerUpdate 触发更新（带并发防护）
func (m *Manager) TriggerUpdate() error {
	_, err, _ := updateGroup.Do("idl-update", func() (interface{}, error) {
		logger.Infof("start IDL update process")
		if err := m.PullRepo(); err != nil {
			logger.Errorf("pull repo failed: %v", err)
			return nil, err
		}
		if err := m.reloadAll(); err != nil {
			logger.Errorf("reload failed: %v, keep old version", err)
			return nil, err
		}
		logger.Infof("IDL update completed")
		return nil, nil
	})
	return err
}

// GetManager 获取全局实例
func GetManager() *Manager {
	return manager
}
