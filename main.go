package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Source struct {
	Username     string
	Token        string
	Organization bool
	Exclude      []string
	Include      []string
}

type Config struct {
	Sources     []*Source
	Destination string
	MaxPackSize int64
}

type Stat struct {
	Source       *Source
	Repos        []*Repo
	Skipped      int
	Mirrored     int
	Updated      int
	Failed       int
	FailedMirror int
	FailedUpdate int
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal("Failed to load config: ", err)
	}

	err = os.MkdirAll(config.Destination, 0755)
	if err != nil {
		if !os.IsExist(err) {
			log.Fatal("Failed to create destination directory: ", err)
		}
	}

	var stats []*Stat
	for _, source := range config.Sources {
		stat := &Stat{
			Source: source,
		}
		stats = append(stats, stat)
		repos, err := getRepo(source)
		if err != nil {
			log.Printf("Failed to get source [%s] repos. error:'%s'", source.Username, err)
			continue
		}
		stat.Repos = repos
		log.Printf("Found %d repos for source [%s]", len(repos), source.Username)
		for _, repo := range repos {
			remote := fmt.Sprintf("https://github.com/%s.git", repo.FullName)
			local := fmt.Sprintf("%s.git", filepath.Join(config.Destination, "github.com", repo.FullName))
			if skip(source, remote) {
				stat.Skipped++
				continue
			}
			_, err := os.Stat(local)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Printf("Failed to stat [%s]: %s", local, err)
					stat.Failed++
					continue
				}
				url := remote
				if repo.Private {
					url = strings.Replace(remote, "https://", fmt.Sprintf("https://%s:%s@", source.Username, source.Token), 1)
				}
				log.Printf("Mirroring [%s] -> [%s]", remote, local)
				_, err := clone(url, local)
				if err != nil {
					log.Printf("Failed mirror [%s] -> [%s]: clone error:'%s'", remote, local, err)
					remove(local)
					stat.FailedMirror++
					continue
				}
				_, err = touch(local)
				if err != nil {
					log.Printf("Failed mirror [%s] -> [%s]: touch error:'%s'", remote, local, err)
					remove(local)
					stat.FailedMirror++
					continue
				}
				_, err = repack(local, config.MaxPackSize)
				if err != nil {
					log.Printf("Failed mirror [%s] -> [%s]: repack error:'%s'", remote, local, err)
					remove(local)
					stat.FailedMirror++
					continue
				}
				_, err = update(local)
				if err != nil {
					log.Printf("Failed mirror [%s] -> [%s]. update error:'%s'", remote, local, err)
					remove(local)
					stat.FailedMirror++
					continue
				}
				log.Printf("Successfully mirror [%s] -> [%s]", remote, local)
				stat.Mirrored++
			} else {
				log.Printf("Updating [%s] -> [%s]", remote, local)
				_, err := update(local)
				if err != nil {
					log.Printf("Failed update [%s] -> [%s] error: %s", remote, local, err)
					stat.FailedUpdate++
					continue
				}
				log.Printf("Successfully update [%s] -> [%s]", remote, local)
				stat.Updated++
			}
		}
	}
	for _, stat := range stats {
		log.Printf("Source [%s] stats: repos:%d skipped:%d mirrored:%d updated:%d failed:%d failed_mirror:%d failed_update:%d", stat.Source.Username, len(stat.Repos), stat.Skipped, stat.Mirrored, stat.Updated, stat.Failed, stat.FailedMirror, stat.FailedUpdate)
	}
}

func loadConfig() (*Config, error) {
	b, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}
	config := &Config{}
	err = json.Unmarshal(b, config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

type Repo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Private bool `json:"private"`
}

func getRepo(source *Source) ([]*Repo, error) {
	var repos []*Repo
	page := 1
	perPage := 100
	for {
		pageRepos, err := getRepoPage(source, page, perPage)
		if err != nil {
			return nil, err
		}
		if len(pageRepos) == 0 {
			break
		}
		repos = append(repos, pageRepos...)
		page++
	}
	return repos, nil
}

func getRepoPage(source *Source, page, perPage int) ([]*Repo, error) {
	url := "https://api.github.com/user/repos"
	if source.Organization {
		url = "https://api.github.com/orgs/" + source.Username + "/repos"
	}
	url = fmt.Sprintf("%s?page=%d&per_page=%d", url, page, perPage)
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", source.Token))
	req.Header.Add("Accept", "application/vnd.github+json")
	req.Header.Add("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repos []*Repo
	err = json.NewDecoder(resp.Body).Decode(&repos)
	if err != nil {
		return nil, err
	}
	return repos, nil
}

func contains(s []string, e string) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}
	return false
}

func skip(source *Source, remote string) bool {
	if len(source.Include) > 0 && !contains(source.Include, remote) {
		return true
	}
	if contains(source.Exclude, remote) {
		return true
	}
	return false
}

func clone(url, local string) (*exec.Cmd, error) {
	cmd := exec.Command("git", "clone", "--mirror", url, local)
	err := cmd.Run()
	return cmd, err
}

func touch(local string) (*exec.Cmd, error) {
	cmd := exec.Command("touch", filepath.Join(local, "refs", ".gitkeep"))
	err := cmd.Run()
	return cmd, err
}

func repack(local string, maxPackSize int64) (*exec.Cmd, error) {
	if maxPackSize == 0 {
		return nil, nil
	}
	if maxPackSize < 1024*1024 {
		maxPackSize = 1024 * 1024
	}
	_repack := false
	filepath.WalkDir(filepath.Join(local, "objects", "pack"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".pack") {
			return nil
		}
		fi, _err := d.Info()
		if _err != nil {
			return _err
		}
		if fi.Size() >= maxPackSize {
			_repack = true
			return filepath.SkipAll
		}
		return nil
	})
	if !_repack {
		return nil, nil
	}
	size := fmt.Sprintf("%dm", maxPackSize/1024/1024)
	cmd := exec.Command("git", "-C", local, "repack", "--max-pack-size="+size, "-A", "-d")
	err := cmd.Run()
	return cmd, err
}

func update(local string) (*exec.Cmd, error) {
	cmd := exec.Command("git", "-C", local, "remote", "update")
	err := cmd.Run()
	return cmd, err
}

func remove(local string) (*exec.Cmd, error) {
	cmd := exec.Command("rm", "-rf", local)
	err := cmd.Run()
	return cmd, err
}
