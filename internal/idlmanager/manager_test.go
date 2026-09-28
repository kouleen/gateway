package idlmanager

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	gittransport "github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/kouleen/gateway/internal/config"
)

type stubAuthMethod struct {
	name string
}

func (s stubAuthMethod) Name() string {
	return s.name
}

func (s stubAuthMethod) String() string {
	return s.name
}

func TestIsSSHRepoURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		repoURL string
		want    bool
	}{
		{name: "scp style", repoURL: "git@github.com:your-org/idl-repo.git", want: true},
		{name: "ssh scheme", repoURL: "ssh://git@github.com/your-org/idl-repo.git", want: true},
		{name: "https scheme", repoURL: "https://github.com/your-org/idl-repo.git", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSSHRepoURL(tt.repoURL); got != tt.want {
				t.Fatalf("isSSHRepoURL(%q) = %v, want %v", tt.repoURL, got, tt.want)
			}
		})
	}
}

func TestDeriveSSHUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repoURL  string
		override string
		want     string
	}{
		{name: "explicit override", repoURL: "git@github.com:your-org/idl-repo.git", override: "deploy", want: "deploy"},
		{name: "scp style url", repoURL: "git@github.com:your-org/idl-repo.git", want: "git"},
		{name: "ssh scheme with custom user", repoURL: "ssh://deploy@github.com/your-org/idl-repo.git", want: "deploy"},
		{name: "fallback default", repoURL: "ssh://github.com/your-org/idl-repo.git", want: "git"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := deriveSSHUser(tt.repoURL, tt.override); got != tt.want {
				t.Fatalf("deriveSSHUser(%q, %q) = %q, want %q", tt.repoURL, tt.override, got, tt.want)
			}
		})
	}
}

func TestBuildSSHAuthPrefersExplicitKeyPath(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")

	oldByFile := newSSHAuthByFile
	oldAgent := newSSHAgentAuth
	defer func() {
		newSSHAuthByFile = oldByFile
		newSSHAgentAuth = oldAgent
	}()

	newSSHAuthByFile = func(user, pemFile, password string) (gittransport.AuthMethod, error) {
		if user != "git" {
			t.Fatalf("unexpected ssh user %q", user)
		}
		if pemFile != keyPath {
			t.Fatalf("unexpected key path %q", pemFile)
		}
		if password != "secret" {
			t.Fatalf("unexpected key password %q", password)
		}
		return stubAuthMethod{name: "ssh-public-keys"}, nil
	}
	newSSHAgentAuth = func(user string) (gittransport.AuthMethod, error) {
		t.Fatalf("ssh-agent should not be called when explicit key path is configured")
		return nil, nil
	}

	cfg := &config.Config{
		IDLRepoURL:       "git@github.com:your-org/idl-repo.git",
		GitSSHKeyPath:    keyPath,
		GitSSHPassphrase: "secret",
	}

	auth, err := buildSSHAuth(cfg)
	if err != nil {
		t.Fatalf("buildSSHAuth() error = %v", err)
	}
	if auth == nil || auth.Name() != "ssh-public-keys" {
		t.Fatalf("unexpected auth: %#v", auth)
	}
}

func TestBuildSSHAuthReturnsHelpfulErrorWithoutKeyOrAgent(t *testing.T) {
	oldByFile := newSSHAuthByFile
	oldAgent := newSSHAgentAuth
	oldHomeDir := userHomeDir
	defer func() {
		newSSHAuthByFile = oldByFile
		newSSHAgentAuth = oldAgent
		userHomeDir = oldHomeDir
	}()

	userHomeDir = func() (string, error) {
		return t.TempDir(), nil
	}
	newSSHAuthByFile = func(user, pemFile, password string) (gittransport.AuthMethod, error) {
		return nil, errors.New("should not load key")
	}
	newSSHAgentAuth = func(user string) (gittransport.AuthMethod, error) {
		return nil, errors.New("agent unavailable")
	}

	cfg := &config.Config{
		IDLRepoURL: "git@github.com:your-org/idl-repo.git",
	}

	auth, err := buildSSHAuth(cfg)
	if err == nil {
		t.Fatalf("expected error, got auth=%#v", auth)
	}
	if auth != nil {
		t.Fatalf("expected nil auth, got %#v", auth)
	}
	if got := err.Error(); got == "" || !containsAll(got, "GIT_SSH_PRIVATE_KEY_PATH", "ssh-agent") {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
