package cpan

import (
  "fmt"
  "reflect"
  "strconv"
)

type Prerequisites struct {
  List []*Dependency
}
func (p *Prerequisites) SetYAML(tag string, value interface{}) bool {
  depmap, ok := value.(map[interface {}]interface {}) 
  if ! ok {
    return false
  }

  for k, v := range depmap {
    // Since v could be numeric, we need to force it to be a string
    t := reflect.ValueOf(v)
    switch t.Type().Kind() {
    case reflect.Float32, reflect.Float64:
      v = strconv.FormatFloat(t.Float(), 'f', -1, 64)
    case reflect.String:
      // nothing to do
    default:
      return false
    }
    p.List = append(p.List, &Dependency{ k.(string), v.(string), false, nil })
  }
  return true
}

func (p *Prerequisites) FulfillRequirements() error {
  for _, x := range p.List {
    fmt.Printf("require %s, version %s\n", x.Name, x.Version)
  }
  return nil
}

