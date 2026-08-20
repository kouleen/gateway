package idlmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// initRepo 初始化仓库，不存在则克隆
func (m *Manager) initRepo() error {
	if _, err := os.Stat(m.cfg.IDLLocalPath); os.IsNotExist(err) {
		logger.Infof("cloning IDL repository...")
		repo, err := git.PlainClone(m.cfg.IDLLocalPath, false, &git.CloneOptions{
			URL:           m.cfg.IDLRepoURL,
			ReferenceName: plumbing.NewBranchReferenceName(m.cfg.IDLRepoBranch),
			Depth:         1, // --depth=1 浅克隆
			Auth:          m.getGitAuth(),
		})
		if err != nil {
			return fmt.Errorf("git clone failed: %w", err)
		}
		m.gitRepo = repo // 保存，后续pull复用，如果你想优化，可以把 *git.Repository 存到 Manager结构体
		return nil
	}

	// 已存在则拉取最新
	return m.pullRepo()
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

// pullRepo 拉取最新代码
func (m *Manager) pullRepo() error {
	if m.gitRepo == nil {
		repo, err := git.PlainOpen(m.cfg.IDLLocalPath)
		if err != nil {
			return fmt.Errorf("open repo failed: %w", err)
		}
		m.gitRepo = repo
	}
	workTree, err := m.gitRepo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree failed: %w", err)
	}

	// pull = fetch + merge
	err = workTree.Pull(&git.PullOptions{
		ReferenceName: plumbing.NewBranchReferenceName(m.cfg.IDLRepoBranch),
	})
	if err != nil {
		// 没有更新时 go‑git 返回 git.NoErrAlreadyUpToDate，不算错误
		if err == git.NoErrAlreadyUpToDate {
			logger.Infof("IDL repo already up‑to‑date")
			return nil
		}
		return fmt.Errorf("git pull failed: %w", err)
	}
	logger.Infof("IDL repo pulled latest version")
	return nil
}

//// initRepo 初始化仓库，不存在则克隆
//func (m *Manager) initRepo() error {
//	if _, err := os.Stat(m.cfg.IDLLocalPath); os.IsNotExist(err) {
//		log.Println("cloning IDL repository...")
//		cmd := exec.Command("git", "clone", "--depth", "1", "-b", m.cfg.IDLRepoBranch, m.cfg.IDLRepoURL, m.cfg.IDLLocalPath)
//		cmd.Env = append(os.Environ(), "GIT_HTTP_CONNECT_TIMEOUT=15", "GIT_HTTP_TIMEOUT=20")
//		cmd.Stdout = os.Stdout
//		cmd.Stderr = os.Stderr
//		if err := cmd.Run(); err != nil {
//			return fmt.Errorf("git clone failed: %w", err)
//		}
//		return nil
//	}
//	// 已存在则先拉取一次
//	return m.pullRepo()
//}
//
//// pullRepo 拉取最新代码
//func (m *Manager) pullRepo() error {
//	cmd := exec.Command("git", "-C", m.cfg.IDLLocalPath, "pull", "origin", m.cfg.IDLRepoBranch)
//	cmd.Stdout = os.Stdout
//	cmd.Stderr = os.Stderr
//	if err := cmd.Run(); err != nil {
//		return fmt.Errorf("git pull failed: %w", err)
//	}
//	logger.Infof("IDL repo pulled latest version")
//	return nil
//}

// reloadAll 重新加载所有客户端和路由
func (m *Manager) reloadAll() error {
	// 1. 读取配置文件
	servicesPath := filepath.Join(m.cfg.IDLLocalPath, "/gateway/services.yaml")
	servicesData, err := os.ReadFile(servicesPath)
	if err != nil {
		return fmt.Errorf("read services.yaml failed: %w", err)
	}

	var svcCfg config.ServersConfig
	if err := yaml.Unmarshal(servicesData, &svcCfg); err != nil {
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
		if err := m.pullRepo(); err != nil {
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
