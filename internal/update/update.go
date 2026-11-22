package update

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	PkgName  = "luci-app-tomato-tunnel"
	XrayPkg  = "tomato-tunnel-xray"
	XrayPath = "/usr/libexec/tomato-tunnel/xray"
	XrayNeed = 40 << 20
)

func XrayInstalled() bool {
	st, err := os.Stat(XrayPath)
	return err == nil && st.Mode().IsRegular()
}

func freeBytes(path string) (int64, bool) {
	var fs syscall.Statfs_t
	if syscall.Statfs(path, &fs) != nil {
		return 0, false
	}
	return int64(fs.Bavail) * int64(fs.Bsize), true
}

func InstallXray(cl *http.Client, r *Result, w io.Writer) error {
	if free, ok := freeBytes("/usr/libexec"); ok && free < XrayNeed {
		return fmt.Errorf("not enough flash space: %d MB free, Xray needs about %d MB", free>>20, XrayNeed>>20)
	}
	if err := installPkg(cl, r, XrayPkg, w); err != nil {
		return err
	}
	fmt.Fprintf(w, "installed Xray from v%s\n", r.Latest)
	return nil
}

func RemoveXray(w io.Writer) error {
	mgr, _ := PkgManager()
	var cmd *exec.Cmd
	if mgr == "apk" {
		cmd = exec.Command("apk", "del", XrayPkg)
	} else {
		cmd = exec.Command("opkg", "remove", XrayPkg)
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %v", mgr, err)
	}
	fmt.Fprintln(w, "removed Xray")
	return nil
}

var File = func() string {
	if d := os.Getenv("TT_RUNDIR"); d != "" {
		return d + "/update.json"
	}
	return "/var/run/tomato-tunnel/update.json"
}()

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Result struct {
	Available bool    `json:"available"`
	Current   string  `json:"current"`
	Latest    string  `json:"latest"`
	Pre       bool    `json:"prerelease"`
	Notes     string  `json:"notes"`
	Page      string  `json:"page"`
	Published string  `json:"published"`
	Checked   int64   `json:"checked"`
	Assets    []Asset `json:"assets,omitempty"`
	Error     string  `json:"error,omitempty"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Body       string  `json:"body"`
	HTMLURL    string  `json:"html_url"`
	Published  string  `json:"published_at"`
	Assets     []Asset `json:"assets"`
}

func Check(cl *http.Client, repo, channel, current string) (*Result, error) {
	repo = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(repo), "https://"), "github.com/"), "/")
	if strings.Count(repo, "/") != 1 {
		return nil, fmt.Errorf("repository must look like owner/name")
	}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases?per_page=10", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tomato-tunnel/"+current)
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("repository %s not found or has no releases", repo)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var rels []release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return nil, err
	}
	for _, r := range rels {
		if r.Draft || (r.Prerelease && channel != "beta") {
			continue
		}
		latest := strings.TrimPrefix(r.Tag, "v")
		return &Result{
			Available: Newer(latest, current), Current: current, Latest: latest, Pre: r.Prerelease,
			Notes: r.Body, Page: r.HTMLURL, Published: r.Published, Checked: time.Now().Unix(), Assets: r.Assets,
		}, nil
	}
	return &Result{Current: current, Latest: current, Checked: time.Now().Unix()}, nil
}

func Save(r *Result) {
	b, _ := json.Marshal(r)
	os.WriteFile(File+".tmp", b, 0644)
	os.Rename(File+".tmp", File)
}

func Newer(a, b string) bool {
	pa, sa := parse(a)
	pb, sb := parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	if sa == sb {
		return false
	}
	if sa == "" {
		return true
	}
	if sb == "" {
		return false
	}
	return sa > sb
}

func parse(v string) ([3]int, string) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	suf := ""
	if i := strings.IndexAny(v, "-+~"); i >= 0 {
		v, suf = v[:i], v[i+1:]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out, suf
}

func PkgManager() (string, []string) {
	if _, err := os.Stat("/etc/apk/arch"); err == nil {
		b, _ := os.ReadFile("/etc/apk/arch")
		var archs []string
		for _, a := range strings.Fields(string(b)) {
			archs = append(archs, a)
		}
		return "apk", append(archs, fallbackArch())
	}
	out, _ := exec.Command("opkg", "print-architecture").Output()
	type pa struct {
		name string
		prio int
	}
	var list []pa
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 && f[0] == "arch" && f[1] != "all" && f[1] != "noarch" {
			p, _ := strconv.Atoi(f[2])
			list = append(list, pa{f[1], p})
		}
	}
	for i := range list {
		for j := i + 1; j < len(list); j++ {
			if list[j].prio > list[i].prio {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	var archs []string
	for _, x := range list {
		archs = append(archs, x.name)
	}
	return "opkg", append(archs, fallbackArch())
}

func Install(cl *http.Client, r *Result, w io.Writer) error {
	xr := XrayInstalled()
	if err := installPkg(cl, r, PkgName, w); err != nil {
		return err
	}
	if xr {
		if err := installPkg(cl, r, XrayPkg, w); err != nil {
			fmt.Fprintf(w, "Xray add-on not updated: %v\n", err)
		}
	}
	fmt.Fprintf(w, "installed v%s\n", r.Latest)
	return nil
}

func installPkg(cl *http.Client, r *Result, name string, w io.Writer) error {
	mgr, archs := PkgManager()
	ext := ".ipk"
	if mgr == "apk" {
		ext = ".apk"
	}
	var pkg, sums *Asset
	for _, arch := range archs {
		for i, a := range r.Assets {
			if strings.HasPrefix(a.Name, name+"_") && strings.HasSuffix(a.Name, "_"+arch+ext) {
				pkg = &r.Assets[i]
				break
			}
		}
		if pkg != nil {
			break
		}
	}
	for i, a := range r.Assets {
		if a.Name == "sha256sums.txt" || a.Name == "sha256sums" {
			sums = &r.Assets[i]
		}
	}
	if pkg == nil {
		return fmt.Errorf("release v%s has no %s %s package for %s", r.Latest, name, ext, strings.Join(archs, "/"))
	}
	if sums == nil {
		return fmt.Errorf("release v%s has no sha256sums.txt, refusing to install", r.Latest)
	}
	fmt.Fprintf(w, "downloading %s\n", pkg.Name)
	dst := "/tmp/" + pkg.Name
	sum, err := download(cl, pkg.URL, dst)
	if err != nil {
		return err
	}
	resp, err := cl.Get(sums.URL)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	want := ""
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == pkg.Name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" || want != sum {
		os.Remove(dst)
		return fmt.Errorf("SHA256 mismatch for %s (got %s, want %s)", pkg.Name, sum, want)
	}
	fmt.Fprintf(w, "SHA256 OK %s\n", sum)
	var cmd *exec.Cmd
	if mgr == "apk" {
		cmd = exec.Command("apk", "add", "--allow-untrusted", dst)
	} else {
		cmd = exec.Command("opkg", "install", dst)
	}
	cmd.Stdout, cmd.Stderr = w, w
	err = cmd.Run()
	os.Remove(dst)
	if err != nil {
		return fmt.Errorf("%s failed: %v", mgr, err)
	}
	return nil
}

func download(cl *http.Client, url, dst string) (string, error) {
	resp, err := cl.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 64<<20))
	f.Close()
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fallbackArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "mipsle":
		return "mipsel_24kc"
	case "mips":
		return "mips_24kc"
	case "arm":
		return "arm_cortex-a7"
	}
	return "aarch64_generic"
}
