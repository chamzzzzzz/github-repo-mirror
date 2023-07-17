package main

import (
	"encoding/json"
	"fmt"
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

	for _, source := range config.Sources {
		repos, err := getRepo(source)
		if err != nil {
			log.Fatalf("Failed to get source %s: %s", source.Username, err)
		}
		log.Printf("Found %d repos for source %s", len(repos), source.Username)
		for _, repo := range repos {
			remote := fmt.Sprintf("https://github.com/%s.git", repo.FullName)
			local := fmt.Sprintf("%s.git", filepath.Join(config.Destination, "github.com", repo.FullName))
			if skip(source, remote) {
				log.Printf("Skipping [%s]", remote)
				continue
			}
			_, err := os.Stat(local)
			if err != nil {
				if !os.IsNotExist(err) {
					log.Fatalf("Failed to stat [%s]: %s", local, err)
				}
				log.Printf("mirror clone [%s] to [%s]", remote, local)
				url := remote
				if repo.Private {
					url = strings.Replace(remote, "https://", fmt.Sprintf("https://%s:%s@", source.Username, source.Token), 1)
				}
				cmd := exec.Command("git", "clone", "--quiet", "--mirror", url, local)
				err := cmd.Run()
				if err != nil {
					log.Fatalf("Failed to mirror clone [%s] to [%s]: %s", remote, local, err)
				}
				log.Printf("Successfully mirror cloned [%s] to [%s]", remote, local)
			}
			log.Printf("mirror update [%s] to [%s]", remote, local)
			cmd := exec.Command("git", "-C", local, "remote", "update", "--prune")
			err = cmd.Run()
			if err != nil {
				log.Fatalf("Failed to mirror clone [%s] to [%s]: %s", remote, local, err)
			}
			log.Printf("Successfully mirror updated [%s] to [%s]", remote, local)
		}
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
