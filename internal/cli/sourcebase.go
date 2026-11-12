package cli

import (
	"fmt"
	"io"
	"strings"

	"yardr.dev/yardr/internal/source"
	"yardr.dev/yardr/internal/store"
)

// Where a source's calls go, in the words the source commands print: its
// address with the base or the host that chose it (store.Source.CalledAt),
// the other bases its file names, and the template its host would fill.

// baseState is where the source's calls go as yardr source set prints it after
// a base or a host was set.
func baseState(d *source.Declaration, src *store.Source) string {
	if addr := src.CalledAt(d); addr != "" {
		return "calls go to " + addr
	}
	return "the file declares no call"
}

// basesLine lists the region bases the file declares beside its own base,
// in a line for yardr source show; "" for a service that runs in one
// region.
func basesLine(d *source.Declaration) string {
	if len(d.Bases) == 0 {
		return ""
	}
	var each []string
	for _, name := range d.BaseNames() {
		each = append(each, name+" "+d.Bases[name])
	}
	return "bases: " + strings.Join(each, ", ")
}

// hostLine says in a line for yardr source show what the file's template lets
// the operator do: the address a host they type would send the calls to, and
// the host the source is set to where it is set one. "" for a file that names
// no template.
func hostLine(d *source.Declaration, src *store.Source) string {
	if d.BaseTemplate == "" {
		return ""
	}
	template := strings.Replace(d.BaseTemplate, source.HostMark, "<host>", 1)
	if src.Host != "" {
		return "host: " + src.Host + ", which fills " + template + " (yardr source set " + d.Name + " host= sends the calls to the declaration's own base again)"
	}
	return "host: none set; yardr source set " + d.Name + " host=<host> sends the calls to " + template + " for a server of your own"
}

// hostFlag is " --host <host>" for the command that approves a setup with a
// host, as the gate prints it for the operator to run again, and nothing where
// none is given.
func hostFlag(host string) string {
	if host == "" {
		return ""
	}
	return " --host " + shellQuote(host)
}

// printHostGiven says who gave the host an approval carried, and nothing where
// it carried none.
func printHostGiven(w io.Writer, who, host string) {
	if host != "" {
		fmt.Fprintf(w, "%s gave the host %s\n", who, host)
	}
}
