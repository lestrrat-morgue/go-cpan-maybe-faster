package cpan

import (
  "archive/tar"
  "compress/gzip"
  "fmt"
  "strings"
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
  Queue chan *Request
  Dependencies map[string]*sync.WaitGroup
}

func NewClient() *Client {
  c := &Client {
    make(chan *Request, 1),
    make(map[string]*sync.WaitGroup),
  }
  go c.ProcessQueue()
  return c
}

func (c *Client) Install(d *Dependency) error {
  wg := &sync.WaitGroup {}
  wg.Add(1)
  c.install(d, wg)
  wg.Wait()

  if d.Success {
    return nil
  }

  return d.Error
}

func (c *Client) install(d *Dependency, wg *sync.WaitGroup) {
  c.Queue <-&Request { d, wg }
}

func (c *Client) ProcessQueue() {
  for r := range c.Queue {
    if r.Name == "perl" {
      fmt.Fprintf(os.Stderr, "%s is not supported, skipping\n", r.Name)
      r.Wait.Done()
      continue
    }
    if _, ok := c.Dependencies[r.Name]; ok {
//      fmt.Fprintf(os.Stderr, "%s has already been requested, skipping\n", r.Name)
      r.Wait.Done()
      continue
    }
    fmt.Printf("Processing %s\n", r.Name)
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

  c.Fetch(name)

  d := NewDistribution(name)
  if err = d.Install(c); err != nil {
    fmt.Printf("failed to install %s: %s\n", name, err)
  } else {
    r.Success = true
  }

  fmt.Printf("DONE: %s\n", name)
  c.Dependencies[r.Name].Done()
}

func (c *Client) Fetch(path string) {
}

func (c *Client) ResolveDistributionName(name string) (string, error) {
  res, err := http.Get("http://cpanmetadb.plackperl.org/v1.0/package/" + name)
  if err != nil {
    return "", err
  }

  body, err := ioutil.ReadAll(res.Body)
  if err != nil {
    return  "", err
  }

  var result map[string]string
  err = yaml.Unmarshal([]byte(body), &result)
  if err != nil {
    return "", err
  }
  return result["distfile"], nil
}

type Distribution struct {
  Path    string
  WorkDir string
  Meta    *Distmeta
}

func NewDistribution(path string) *Distribution {
  return &Distribution {
    path,
    "",
    nil,
  }
}

func (d *Distribution) Save(r io.Reader) error {
  dir := path.Dir(d.Path)
  if _, err := os.Stat(dir); err != nil {
    if err = os.MkdirAll(dir, 0777); err != nil {
      return err
    }
  }

  fh, err := os.OpenFile(d.Path, os.O_CREATE|os.O_WRONLY, 0777)
  if err != nil {
    return err
  }
  defer fh.Close()

  if _, err = io.Copy(fh, r); err != nil {
    return err
  }

  return nil
}

func (d *Distribution) Install(c *Client) error {
  _, err := os.Stat(d.Path)
  if err != nil {
    fmt.Printf("Installing %s...\n", d.Path)
    res, err := http.Get("http://cpan.metacpan.org/authors/id/" + d.Path)
    if err != nil {
      return err
    }

    d.Save(res.Body)
    _, err = os.Stat(d.Path)
    if err != nil {
      return err
    }
  }

  if err = d.Unpack(); err != nil {
    return fmt.Errorf("error during unpack: %s", err)
  }

  if err = d.ParseMeta(); err != nil {
    return err
  }

  wg := &sync.WaitGroup {}
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

  waitch := make(chan struct{})
  go func() {
    defer func() { waitch <- struct{}{} }()
    fmt.Printf("CMD: cpanm %s\n", d.WorkDir)
    cmd := exec.Command("cpanm", "-n", "-L", "local", d.WorkDir)
    output, _ := cmd.CombinedOutput()
    os.Stdout.Write(output)
  }();
  <-waitch

  return nil
}

func (d *Distribution) Unpack()  error {
  fmt.Printf("Unpacking %s\n", d.Path)
  done := false
  root := ""
  defer func() {
    if ! done && root != "" {
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

fmt.Printf("Unpack -> root = %s\n", root)
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
