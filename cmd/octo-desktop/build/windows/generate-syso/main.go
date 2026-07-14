// generate-syso builds Windows resource objects (rsrc_windows_*.syso) that
// embed the application icon and manifest into the octo-desktop.exe binary.
// It is invoked by CI before the Windows go build; the .syso files are build
// artifacts and are gitignored.
package main

import (
	"fmt"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: generate-syso <arch> <icon.ico> <manifest.xml>\n")
		os.Exit(1)
	}
	arch, iconPath, manifestPath := os.Args[1], os.Args[2], os.Args[3]

	rs := winres.ResourceSet{}

	iconFile, err := os.Open(iconPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open icon: %v\n", err)
		os.Exit(1)
	}
	ico, err := winres.LoadICO(iconFile)
	iconFile.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load icon: %v\n", err)
		os.Exit(1)
	}
	if err := rs.SetIcon(winres.RT_ICON, ico); err != nil {
		fmt.Fprintf(os.Stderr, "set icon: %v\n", err)
		os.Exit(1)
	}

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read manifest: %v\n", err)
		os.Exit(1)
	}
	xmlData, err := winres.AppManifestFromXML(manifestData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse manifest: %v\n", err)
		os.Exit(1)
	}
	rs.SetManifest(xmlData)

	out, err := os.Create("rsrc_windows_" + arch + ".syso")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create syso: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	archMap := map[string]winres.Arch{
		"amd64": winres.ArchAMD64,
		"arm64": winres.ArchARM64,
		"386":   winres.ArchI386,
	}
	a, ok := archMap[arch]
	if !ok {
		fmt.Fprintf(os.Stderr, "unsupported arch: %s\n", arch)
		os.Exit(1)
	}
	if err := rs.WriteObject(out, a); err != nil {
		fmt.Fprintf(os.Stderr, "write syso: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated rsrc_windows_%s.syso\n", arch)
}
