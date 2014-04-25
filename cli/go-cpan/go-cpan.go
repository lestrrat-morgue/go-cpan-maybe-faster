package main

import (
	"flag"
	"github.com/lestrrat/go-cpan-maybe-faster"
)

func main() {
	notest := flag.Bool("notest", false, "Do not run unit tests")
	verbose := flag.Bool("verbose", false, "be verbose")
	localLib := flag.String("local-lib", "", "Specify the install base to install modules")
	localLibContained := flag.String("local-lib-contained", "", "Specify the install base to install all non-core modules")
	flag.Parse()

	c := cpan.NewClient()
	c.SetVerbose(*verbose)
	c.SetNotest(*notest)
	c.SetLocalLib(*localLib)
	c.SetLocalLibContained(*localLibContained)

	args := flag.Args()
	for i := 0; i < len(args); i++ {
		c.Install(args[i])
	}
}
