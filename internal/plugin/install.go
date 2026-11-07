package plugin

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mygo-music/internal/local"
)

// Plugins install from GitHub: a repository's latest release, as the zip it
// offers for this platform, or for all, or else its source; without a release,
// the latest commit of its default branch. plugin.json is at the root of what
// that holds, or of its one folder, as GitHub's own archives have it. They
// install from this machine too, from a plugin's folder or a zip of one.

// originFile records in a plugin's folder where it was installed from.
const originFile = ".origin.json"

const (
	// maxArchive and maxUnpacked bound what an archive may take, for one that
	// isn't what it says not to fill the disk.
	maxArchive  = 1 << 30
	maxUnpacked = 2 << 30
	maxFiles    = 50000
)

// Origin is where a plugin was installed from.
type Origin struct {
	Repo    string `json:"repo"` // owner/repo
	Version string `json:"version"`
}

// Release is what a repository offers to install.
type Release struct {
	Repo    string
	Version string // the tag, or the commit
	url     string // of the archive
}

// Pending is a plugin downloaded and checked, waiting for the listener to say
// whether to install it.
type Pending struct {
	Release
	// Path is the folder or zip it came from, for one from this machine, which
	// has no Repo.
	Path     string
	Manifest Manifest
	root     string // where it unpacked
	tmp      string // the folder to remove with it
}

// ErrNoRepo says there's no such public repository.
var ErrNoRepo = errors.New("no such public repository on GitHub")

// githubAPI is GitHub's API; tests replace it, and installClient.
var (
	githubAPI     = "https://api.github.com"
	installClient = &http.Client{Timeout: 10 * time.Minute}
)

var (
	repoName   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	pluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// ParseRepo reads a repository as the listener may write it: owner/repo, or its
// address on GitHub, as copied from the browser on any of its pages, such as
// github.com/owner/repo/tree/main, or to clone it.
func ParseRepo(in string) (string, error) {
	s := strings.TrimSpace(in)
	if rest, ok := cutPrefixFold(s, "git@github.com:"); ok {
		s = rest
	} else {
		if rest, ok := cutPrefixFold(s, "https://"); ok {
			s = rest
		} else if rest, ok := cutPrefixFold(s, "http://"); ok {
			s = rest
		}
		if rest, ok := cutPrefixFold(s, "www."); ok {
			s = rest
		}
		if rest, ok := cutPrefixFold(s, "github.com/"); ok {
			// A page of the repository's: its first two parts name it.
			rest, _, _ = strings.Cut(rest, "#")
			rest, _, _ = strings.Cut(rest, "?")
			if parts := strings.SplitN(rest, "/", 3); len(parts) == 3 {
				rest = parts[0] + "/" + parts[1]
			}
			s = rest
		}
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	if !repoName.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("not a GitHub repository: %s", strings.TrimSpace(in))
	}
	return s, nil
}

// cutPrefixFold is strings.CutPrefix, ignoring case.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// githubToken is the listener's own token for GitHub, if they have one, which
// GitHub's API is asked with: it allows 5,000 requests an hour with one, and 60
// without, from all on the same network, and private repositories install too.
// Tests replace it.
var githubToken = sync.OnceValue(findGitHubToken)

// tokenRefused is whether GitHub turned the token down, as it does one that's
// expired or revoked, for the app to go on without it.
var tokenRefused atomic.Bool

// findGitHubToken looks for a token in GITHUB_TOKEN or GH_TOKEN, as GitHub's own
// tools do, and then asks GitHub's CLI for the one it's signed in with.
func findGitHubToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(name)); t != "" {
			return t
		}
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		// An app opened from the Finder has a PATH without Homebrew's.
		for _, path := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
			if _, err := os.Stat(path); err == nil {
				gh = path
				break
			}
		}
	}
	if gh == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", "github.com")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// withToken is whether GitHub's API is asked with the listener's token.
func withToken() bool { return !tokenRefused.Load() && githubToken() != "" }

// githubSend gets url, as the listener when it's GitHub's API and they have a
// token, which goes nowhere else. One GitHub refuses is dropped, and the request
// sent again without it.
func githubSend(url, accept string) (*http.Response, error) {
	token := ""
	if strings.HasPrefix(url, githubAPI+"/") && withToken() {
		token = githubToken()
	}
	for {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "mygo-music")
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := installClient.Do(req)
		if err != nil || token == "" || res.StatusCode != http.StatusUnauthorized {
			return res, err
		}
		res.Body.Close()
		log.Print("GitHub refused the token; going on without it")
		tokenRefused.Store(true)
		token = ""
	}
}

func githubGet(path string, out any) (status int, err error) {
	res, err := githubSend(githubAPI+path, "application/vnd.github+json")
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return res.StatusCode, json.NewDecoder(res.Body).Decode(out)
	case http.StatusNotFound:
		return res.StatusCode, nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		return res.StatusCode, errors.New("GitHub says to slow down; try again in a while")
	}
	return res.StatusCode, fmt.Errorf("GitHub answered %s", res.Status)
}

// Latest finds what a repository offers now.
func Latest(repo string) (*Release, error) {
	var rel struct {
		Tag    string `json:"tag_name"`
		Zip    string `json:"zipball_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			API  string `json:"url"`
		} `json:"assets"`
	}
	status, err := githubGet("/repos/"+repo+"/releases/latest", &rel)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		names := make([]string, len(rel.Assets))
		for i, a := range rel.Assets {
			names[i] = a.Name
		}
		url := rel.Zip
		if at := pickAsset(names, runtime.GOOS, runtime.GOARCH); at >= 0 {
			url = rel.Assets[at].URL
			// A private repository's assets download from the API alone.
			if withToken() && rel.Assets[at].API != "" {
				url = rel.Assets[at].API
			}
		}
		return &Release{Repo: repo, Version: rel.Tag, url: url}, nil
	}
	// No release: the latest commit of its default branch.
	var commit struct {
		SHA string `json:"sha"`
	}
	status, err = githubGet("/repos/"+repo+"/commits/HEAD", &commit)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || commit.SHA == "" {
		return nil, ErrNoRepo
	}
	return &Release{Repo: repo, Version: commit.SHA[:min(len(commit.SHA), 12)], url: githubAPI + "/repos/" + repo + "/zipball/" + commit.SHA}, nil
}

// pickAsset chooses the zip for a platform: the one named for it, or else one
// named for none. A release of zips for other platforms alone has none to offer,
// and -1 leaves its source.
func pickAsset(names []string, goos, goarch string) int {
	platforms := []string{"darwin", "linux", "windows"}
	general := -1
	for i, name := range names {
		n := strings.ToLower(name)
		if !strings.HasSuffix(n, ".zip") {
			continue
		}
		if strings.Contains(n, goos) && strings.Contains(n, goarch) {
			return i
		}
		other := false
		for _, p := range platforms {
			other = other || strings.Contains(n, p)
		}
		if !other && general < 0 {
			general = i
		}
	}
	return general
}

// Fetch downloads and unpacks what a repository offers now into dir, the plugins'
// folder, without installing it, and checks it's a plugin that compiles. Nothing
// of it runs. Discard or Install it next.
func Fetch(repo, dir string) (*Pending, error) {
	rel, err := Latest(repo)
	if err != nil {
		return nil, err
	}
	return FetchRelease(rel, dir)
}

// FetchRelease is Fetch of a release Latest found already, without asking GitHub
// again what there is.
func FetchRelease(rel *Release, dir string) (*Pending, error) {
	return pending(dir, func(tmp string) (*Pending, error) { return fetchInto(rel, tmp) })
}

// pending makes a plugin waiting to be installed, in a folder of its own in dir
// that goes with it if that fails. The folder is in the same one as the
// plugins, for moving it in place to be one step, and its dot leaves it out of
// Discover, if the app quits with it here.
func pending(dir string, into func(tmp string) (*Pending, error)) (*Pending, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, ".installing-")
	if err != nil {
		return nil, err
	}
	p, err := into(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return p, nil
}

func fetchInto(rel *Release, tmp string) (*Pending, error) {
	archive := filepath.Join(tmp, "archive.zip")
	if err := download(rel.url, archive); err != nil {
		return nil, err
	}
	unpacked := filepath.Join(tmp, "unpacked")
	if err := unzip(archive, unpacked); err != nil {
		return nil, fmt.Errorf("unpacking %s: %w", rel.Repo, err)
	}
	return checked(unpacked, tmp, rel.Repo, &Pending{Release: *rel})
}

// checked finds the plugin in what was unpacked, and fills in p with it if it's
// one that compiles, with a name that can be a folder's. Nothing of it runs.
func checked(unpacked, tmp, from string, p *Pending) (*Pending, error) {
	root, err := pluginRoot(unpacked)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", from, err)
	}
	loaded, err := Load(root, filepath.Join(tmp, "data"))
	if err != nil {
		return nil, err
	}
	id := loaded.manifest.ID
	if id == local.ID || !pluginName.MatchString(id) {
		return nil, fmt.Errorf("%s: a plugin can't be called %q", from, id)
	}
	p.Manifest, p.root, p.tmp = loaded.manifest, root, tmp
	return p, nil
}

// Open copies a plugin from this machine, its folder or a zip of one, into dir,
// the plugins' folder, without installing it, and checks it as Fetch does. Its
// version is what plugin.json says.
func Open(path, dir string) (*Pending, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	return pending(dir, func(tmp string) (*Pending, error) {
		unpacked := filepath.Join(tmp, "unpacked")
		if info.IsDir() {
			err = copyPlugin(path, unpacked)
		} else {
			err = unzip(path, unpacked)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		p, err := checked(unpacked, tmp, name, &Pending{Path: path})
		if err != nil {
			return nil, err
		}
		p.Version = p.Manifest.Version
		return p, nil
	})
}

// copyPlugin copies a plugin's folder to dir, as unzip unpacks one: within
// the same bounds, without links, and keeping programs executable. What's
// hidden stays behind, such as .git, and folders being installed, if dir is
// in it.
func copyPlugin(from, dir string) error {
	var files int
	var total int64
	return filepath.WalkDir(from, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if rel != "." && strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		to := filepath.Join(dir, rel)
		if e.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("a link in the plugin: %s", filepath.ToSlash(rel))
		}
		if e.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if !e.Type().IsRegular() {
			return nil
		}
		if files++; files > maxFiles {
			return errors.New("too many files")
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			perm = 0o755
		}
		n, err := copyFile(path, to, perm, int64(maxUnpacked)-total)
		if err != nil {
			return err
		}
		if total += n; total > maxUnpacked {
			return errors.New("the plugin is too large")
		}
		return nil
	})
}

func copyFile(from, to string, perm os.FileMode, limit int64) (int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	// One byte over says it's too large, rather than cutting it short.
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return n, err
	}
	return n, out.Close()
}

func download(url, path string) error {
	// An asset from the API comes as itself with this, and as its description
	// without; a zipball refuses it.
	accept := ""
	if strings.Contains(url, "/releases/assets/") {
		accept = "application/octet-stream"
	}
	res, err := githubSend(url, accept)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading the plugin: %s", res.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(res.Body, maxArchive+1))
	if err != nil {
		return err
	}
	if n > maxArchive {
		return errors.New("the plugin's archive is too large")
	}
	return f.Close()
}

// unzip unpacks an archive into dir, refusing anything that would land outside
// it. Links go too, as they could point anywhere. A file keeps being executable:
// a plugin's programs have to be.
func unzip(archive, dir string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	if len(z.File) > maxFiles {
		return errors.New("too many files")
	}
	var total int64
	for _, f := range z.File {
		name := filepath.FromSlash(f.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("a file outside the plugin: %s", f.Name)
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("a link in the plugin: %s", f.Name)
		}
		path := filepath.Join(dir, name)
		if mode.IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		left := int64(maxUnpacked) - total
		n, err := unpackFile(f, path, perm, left)
		if err != nil {
			return err
		}
		if total += n; total > maxUnpacked {
			return errors.New("the plugin is too large")
		}
	}
	return nil
}

func unpackFile(f *zip.File, path string, perm os.FileMode, limit int64) (int64, error) {
	in, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	// One byte over says it's too large, rather than cutting it short.
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return n, err
	}
	return n, out.Close()
}

// pluginRoot finds plugin.json: at the top, or in the one folder there.
func pluginRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var folders []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			folders = append(folders, e.Name())
		}
	}
	if len(folders) == 1 {
		root := filepath.Join(dir, folders[0])
		if _, err := os.Stat(filepath.Join(root, "plugin.json")); err == nil {
			return root, nil
		}
	}
	return "", errors.New("it has no plugin.json at its top: it isn't a plugin")
}

// IsNewer reports whether a repository's version is one to update to from what's
// installed. Tags that are version numbers compare as such, for a release taken
// back not to read as new. Anything else has no order: a commit is just the latest,
// so one that's different is new.
func IsNewer(latest, installed string) bool {
	if latest == installed {
		return false
	}
	a, okA := versionNumbers(latest)
	b, okB := versionNumbers(installed)
	if !okA || !okB {
		return true
	}
	for i := range max(len(a), len(b)) {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// versionNumbers reads a tag such as v1.2.3, or 1.2. A tag with a label, such as
// 1.2.3-beta, isn't one: its order isn't only in its numbers.
func versionNumbers(tag string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}

// Discard forgets a plugin that won't be installed.
func (p *Pending) Discard() { os.RemoveAll(p.tmp) }

// Install puts the plugin in the plugins' folder dir, in place of the one it
// replaces, if any, whose data stays. A plugin of that name from elsewhere stays
// too: the new one can't take its name, and what it signed in to. One from this
// machine is from nowhere, as one dropped in is, and replaces only such.
func (p *Pending) Install(dir string) error {
	defer os.RemoveAll(p.tmp)
	target := filepath.Join(dir, p.Manifest.ID)
	if _, err := os.Stat(target); err == nil {
		var was Origin
		readJSON(filepath.Join(target, originFile), &was)
		if was.Repo != p.Repo {
			return fmt.Errorf("a plugin called %s is already installed", p.Manifest.ID)
		}
	}
	if p.Repo != "" {
		b, err := json.Marshal(Origin{Repo: p.Repo, Version: p.Version})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(p.root, originFile), b, 0o644); err != nil {
			return err
		}
	}
	// Out of the way, then in, so that what fails leaves the one there.
	old := filepath.Join(p.tmp, "replaced")
	hadOne := false
	if err := os.Rename(target, old); err == nil {
		hadOne = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(p.root, target); err != nil {
		if hadOne {
			os.Rename(old, target)
		}
		return err
	}
	return nil
}

// Remove takes a plugin's folder away once it has stopped, with trash, which moves
// it to the Trash, or deletes it if that can't be done, as on a system with none.
// Its data stays, for it to find if it's installed again.
func (p *Plugin) Remove(trash func(path string) error) error {
	p.Close()
	if trash != nil && trash(p.dir) == nil {
		return nil
	}
	return os.RemoveAll(p.dir)
}

// Dir is the plugin's folder.
func (p *Plugin) Dir() string { return p.dir }

// Origin is where the plugin was installed from, or nil if it was just dropped in.
func (p *Plugin) Origin() *Origin {
	var o Origin
	readJSON(filepath.Join(p.dir, originFile), &o)
	if o.Repo == "" {
		return nil
	}
	return &o
}

// Reloaded reads the plugin in the same place again, as it's been updated. This
// one stops.
func (p *Plugin) Reloaded(dataDir string) (*Plugin, error) {
	p.Close()
	return Load(p.dir, dataDir)
}
