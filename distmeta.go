package cpan

import (
	"gopkg.in/yaml.v1"
	"io/ioutil"
	"os"
)

type Distmeta struct {
	Abstract          string
	Author            []string
	BuildRequires     *Prerequisites
	ConfigureRequires *Prerequisites
	DistributionType  string
	DynamicConfig     bool
	GeneratedBy       string
	License           string
	MetaSpec          map[string]string
	ModuleName        string
	Name              string
	NoIndex           map[string]map[string]string
	Requires          *Prerequisites
	Resources         map[string]string
	Version           string
}

func LoadDistmetaFromFile(filename string) (*Distmeta, error) {
	fh, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	buf, err := ioutil.ReadAll(fh)
	if err != nil {
		return nil, err
	}

	meta := &Distmeta{
		ConfigureRequires: &Prerequisites{},
		Requires:          &Prerequisites{},
	}
	err = yaml.Unmarshal(buf, meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}
