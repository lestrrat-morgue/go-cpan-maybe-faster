package cpan

import (
	"testing"
)

func TestClient(t *testing.T) {
	client := NewClient()
	client.Install(&Dependency{"Moose", "", false, nil})
}
