package cpan

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"log"
	"strings"
	"time"
	//  "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v1"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sync"
)

type Dependency struct {
	Name    string
	Version string
	Success bool
	Error   error
}

type Request struct {
	*Dependency
	Wait *sync.WaitGroup
}

type Client struct {
	Queue             chan *Request
	Dependencies      map[string]*sync.WaitGroup
	DistributionNames map[string]string
	WorkDir           string
	logger            *log.Logger
	notest            bool
	localLib          string
	localLibContained string
}

func NewClient() *Client {
	tmpdir, err := ioutil.TempDir("", "go-cpan-")
	if err != nil {
		panic(err.Error())
	}

	c := &Client{
		make(chan *Request, 1),
		make(map[string]*sync.WaitGroup),
		make(map[string]string),
		tmpdir,
		nil,
		false,
		"",
		"",
	}
	go c.ProcessQueue()
	return c
}

func (c *Client) Logf(format string, args ...interface{}) {
	if c.logger == nil {
		return
	}

	if len(format) > 0 && format[len(format)-1] != '\n' {
		format = format + "\n"
	}

	c.logger.Printf(format, args...)
}

func (c *Client) SetNotest(b bool) {
	c.notest = b
}

func (c *Client) SetLocalLib(l string) {
	if !filepath.IsAbs(l) {
		p, err := filepath.Abs(l)
		if err != nil {
			panic(err.Error())
		}
		l = p
	}
	c.localLib = l
}

func (c *Client) SetLocalLibContained(l string) {
	if !filepath.IsAbs(l) {
		p, err := filepath.Abs(l)
		if err != nil {
			panic(err.Error())
		}
		l = p
	}
	c.localLibContained = l
}

func (c *Client) SetVerbose(b bool) {
	if b && c.logger == nil {
		c.logger = log.New(os.Stderr, "", log.LstdFlags)
	} else {
		c.logger = nil
	}
}

func (c *Client) Install(name string) error {
	return c.InstallDependency(&Dependency{name, "", false, nil})
}

func (c *Client) InstallDependency(d *Dependency) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	err = os.Chdir(c.WorkDir)
	if err != nil {
		return err
	}
	defer os.Chdir(wd)

	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.install(d, wg)
	wg.Wait()

	if d.Success {
		return nil
	}

	return d.Error
}

func (c *Client) install(d *Dependency, wg *sync.WaitGroup) {
	c.Queue <- &Request{d, wg}
}

func (c *Client) ProcessQueue() {
	for r := range c.Queue {
		c.Logf("Working on %s", r.Name)
		if r.Name == "perl" {
			c.Logf("%s is not supported, skipping\n", r.Name)
			r.Wait.Done()
			continue
		}
		if _, ok := c.Dependencies[r.Name]; ok {
			c.Logf("%s has already been requested, skipping\n", r.Name)
			r.Wait.Done()
			continue
		}
		c.Dependencies[r.Name] = r.Wait
		go c.ProcessDependency(r)
	}
}

func (c *Client) ProcessDependency(r *Request) {
	name, err := c.ResolveDistributionName(r.Name)
	if err != nil {
		c.Dependencies[r.Name].Done()
		return
	}

	if strings.Index(name, "/perl-5.") > -1 {
		// skip perl
		c.Dependencies[r.Name].Done()
		return
	}

	done := false
	go func() {
		t := time.Tick(5 * time.Second)
		for _ = range t {
			if done {
				break
			}
			c.Logf("Still waiting for %s...", name)
		}
	}()
	defer func() { done = true }()

	d := NewDistribution(name)
	if err = d.Install(c); err != nil {
		c.Logf("failed to install %s: %s\n", name, err)
	} else {
		r.Success = true
	}

	c.Logf("DONE: %s\n", name)
	c.Dependencies[r.Name].Done()
}

func (c *Client) ResolveDistributionName(name string) (distfile string, err error) {
	defer func() {
		if err == nil {
			c.Logf("cpanmetadb says we can get %s from %s\n", name, distfile)
		}
	}()

	distfile, ok := c.DistributionNames[name]
	if ok {
		return
	}

	res, err := http.Get("http://cpanmetadb.plackperl.org/v1.0/package/" + name)
	if err != nil {
		return "", err
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	var result map[string]string
	err = yaml.Unmarshal([]byte(body), &result)
	if err != nil {
		return "", err
	}

	distfile, ok = result["distfile"]
	if !ok {
		return "", fmt.Errorf("could not find where %s can be found", name)
	}

	return distfile, nil
}

func (c *Client) runCpanm(d *Distribution) {
	waitch := make(chan struct{})
	go func() {
		defer func() { waitch <- struct{}{} }()
		cmdlist := []string{}
		if c.notest {
			cmdlist = append(cmdlist, "--notest")
		}
		if c.localLib != "" {
			cmdlist = append(cmdlist, "--local-lib", c.localLib)
		}
		if c.localLibContained != "" {
			cmdlist = append(cmdlist, "--local-lib-contained", c.localLibContained)
		}
		cmdlist = append(cmdlist, d.WorkDir)

		cmd := exec.Command("cpanm", cmdlist...)

		c.Logf("%v", cmd.Args)
		output, _ := cmd.CombinedOutput()
		os.Stdout.Write(output)
	}()
	<-waitch
}

type Distribution struct {
	Path    string
	WorkDir string
	Meta    *Distmeta
}

func NewDistribution(path string) *Distribution {
	return &Distribution{
		path,
		"",
		nil,
	}
}

func (c *Client) Fetch(d *Distribution) error {
	fullpath := filepath.Join(c.WorkDir, d.Path)
	_, err := os.Stat(fullpath)
	if err == nil { // cache found
		return nil
	}

	url := "http://cpan.metacpan.org/authors/id/" + d.Path
	c.Logf("Fetching %s...\n", url)

	var rdr io.Reader
	for i := 0; i < 5; i++ {
		res, err := http.Get(url)
		if err != nil {
			c.Logf("failed to download from %s: %s", url, err)
			continue
		}
		if res.StatusCode != 200 {
			c.Logf("failed to download from %s: status code = %d", url, res.StatusCode)
			continue
		}

		rdr = res.Body
		break
	}

	if rdr == nil {
		return fmt.Errorf("Failed to download from %s", url)
	}

	dir := path.Dir(fullpath)
	if _, err := os.Stat(dir); err != nil {
		if err = os.MkdirAll(dir, 0777); err != nil {
			return err
		}
	}

	fh, err := os.OpenFile(fullpath, os.O_CREATE|os.O_WRONLY, 0777)
	if err != nil {
		return err
	}
	defer fh.Close()

	if _, err = io.Copy(fh, rdr); err != nil {
		return err
	}

	return nil
}

func (d *Distribution) Install(c *Client) error {
	c.Logf("Installing %s...\n", d.Path)
	if err := c.Fetch(d); err != nil {
		return err
	}

	if err := d.Unpack(); err != nil {
		return fmt.Errorf("error during unpack: %s", err)
	}

	if err := d.ParseMeta(); err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	if br := d.Meta.BuildRequires; br != nil {
		for _, dep := range br.List {
			wg.Add(1)
			c.install(dep, wg)
		}
	}

	if cr := d.Meta.ConfigureRequires; cr != nil {
		for _, dep := range cr.List {
			wg.Add(1)
			c.install(dep, wg)
		}
	}

	if r := d.Meta.Requires; r != nil {
		for _, dep := range r.List {
			wg.Add(1)
			c.install(dep, wg)
		}
	}
	wg.Wait()

	c.runCpanm(d)
	return nil
}

func (d *Distribution) Unpack() error {
	done := false
	root := ""
	defer func() {
		if !done && root != "" {
			os.RemoveAll(root)
		}
	}()

	fh, err := os.Open(d.Path)
	if err != nil {
		return err
	}
	defer fh.Close()

	gzr, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		if root == "" {
			if i := strings.IndexRune(hdr.Name, os.PathSeparator); i > -1 {
				root = hdr.Name[0:i]
			}
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if _, err := os.Stat(hdr.Name); err != nil {
				err = os.MkdirAll(hdr.Name, 0777)
				if err != nil {
					return err
				}
			}
		case tar.TypeReg:
			out, err := os.OpenFile(hdr.Name, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}

			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			return fmt.Errorf("unknown type: %s", err)
		}
	}

	done = true
	abspath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	d.WorkDir = abspath
	return nil
}

func (d *Distribution) ParseMeta() error {
	metayml := filepath.Join(d.WorkDir, "META.yml")
	meta, err := LoadDistmetaFromFile(metayml)
	if err != nil {
		return fmt.Errorf("failed to load file %s for %s: %s", metayml, d.Path, err)
	}
	d.Meta = meta
	return nil
}

func (d *Distribution) Cleanup() {
	os.RemoveAll(d.WorkDir)
}
