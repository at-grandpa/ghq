package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/motemen/ghq/logger"
	"golang.org/x/xerrors"
)

var (
	seen = make(map[string]bool)
	mu   = &sync.Mutex{}
)

func getRepoLock(repoPath string) bool {
	mu.Lock()
	defer func() {
		seen[repoPath] = true
		mu.Unlock()
	}()
	return !seen[repoPath]
}

type getter struct {
	update, shallow, silent, ssh bool
	vcs                          string
}

func (g *getter) get(argURL string) error {
	// If argURL is a "./foo" or "../bar" form,
	// find repository name trailing after github.com/USER/.
	parts := strings.Split(argURL, string(filepath.Separator))
	if parts[0] == "." || parts[0] == ".." {
		if wd, err := os.Getwd(); err == nil {
			path := filepath.Clean(filepath.Join(wd, filepath.Join(parts...)))

			var repoPath string
			roots, err := localRepositoryRoots()
			if err != nil {
				return err
			}
			for _, r := range roots {
				p := strings.TrimPrefix(path, r+string(filepath.Separator))
				if p != path && (repoPath == "" || len(p) < len(repoPath)) {
					repoPath = p
				}
			}

			if repoPath != "" {
				// Guess it
				logger.Log("resolved", fmt.Sprintf("relative %q to %q", argURL, "https://"+repoPath))
				argURL = "https://" + repoPath
			}
		}
	}

	u, err := newURL(argURL)
	if err != nil {
		return xerrors.Errorf("Could not parse URL %q: %w", argURL, err)
	}

	if g.ssh {
		// Assume Git repository if `-p` is given.
		if u, err = convertGitURLHTTPToSSH(u); err != nil {
			return xerrors.Errorf("Could not convet URL %q: %w", u, err)
		}
	}

	remote, err := NewRemoteRepository(u)
	if err != nil {
		return err
	}

	if !remote.IsValid() {
		return fmt.Errorf("Not a valid repository: %s", u)
	}

	return g.getRemoteRepository(remote)
}

// getRemoteRepository clones or updates a remote repository remote.
// If doUpdate is true, updates the locally cloned repository. Otherwise does nothing.
// If isShallow is true, does shallow cloning. (no effect if already cloned or the VCS is Mercurial and git-svn)
func (g *getter) getRemoteRepository(remote RemoteRepository) error {
	remoteURL := remote.URL()
	local, err := LocalRepositoryFromURL(remoteURL)
	if err != nil {
		return err
	}

	path := local.FullPath
	root := local.RootPath
	newPath := false

	_, err = os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			newPath = true
			err = nil
		}
		if err != nil {
			return err
		}
	}

	if newPath {
		logger.Log("clone", fmt.Sprintf("%s -> %s", remoteURL, path))
		var (
			vcs      = vcsRegistry[g.vcs]
			repoPath = path
			repoURL  = remoteURL
		)
		if vcs == nil {
			vcs, repoURL = remote.VCS()
			if vcs == nil {
				return fmt.Errorf("Could not find version control system: %s", remoteURL)
			}
			// Only when repoURL is a subpath of remoteURL, rebuild repoPath.
			// This is because there is a case, for example, golang.org/x is hosted
			// on go.googlesource.com.
			if strings.HasPrefix(remoteURL.String(), strings.TrimSuffix(repoURL.String(), ".git")) {
				repoPath = strings.TrimSuffix(filepath.Join(root, repoURL.Host, repoURL.Path), ".git")
			}
		}
		if getRepoLock(repoPath) {
			return vcs.Clone(repoURL, repoPath, g.shallow, g.silent)
		}
		return nil
	}
	if g.update {
		logger.Log("update", path)
		vcs, repoPath := local.VCS()
		if vcs == nil {
			return fmt.Errorf("failed to detect VCS for %q", path)
		}
		if getRepoLock(repoPath) {
			return vcs.Update(repoPath, g.silent)
		}
		return nil
	}
	logger.Log("exists", path)
	return nil
}
