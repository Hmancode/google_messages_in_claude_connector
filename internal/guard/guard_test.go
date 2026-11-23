// Package guard holds the build-breaking safety checks for gm-reader.
//
// TestLibgmUsage parses every Go file in this repository and fails if:
//  1. any package other than internal/gm imports libgm;
//  2. any code calls a libgm Client method that is banned;
//  3. internal/gm exposes a libgm type (which would let other packages call
//     libgm without importing it);
//  4. a write method is called outside internal/gm/write.go;
//  5. libgm has a Client method this file doesn't classify (so a libgm
//     upgrade that adds methods fails until someone reviews them);
//  6. any code imports "reflect" (which could call methods by name).
package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const libgmModule = "go.mau.fi/mautrix-gmessages"

// readAllowed may be called anywhere inside internal/gm.
var readAllowed = set(
	"SetEventHandler", "Connect", "Disconnect", "IsConnected", "IsLoggedIn",
	"FetchConfig", "StartGaiaPairing", "FinishGaiaPairing",
	"ListConversations", "GetConversation", "FetchMessages", "ListContacts",
	"CurrentSessionID",
)

// writeAllowed may only be called from internal/gm/write.go, which checks the
// user's settings and asks for confirmation first.
var writeAllowed = set("SendMessage", "SendReaction", "GetOrCreateConversation", "UploadMedia")

// bannedStateChanging would change messages, chats, settings or the pairing.
var bannedStateChanging = set(
	"MarkRead",                            // marks chats read on the phone
	"DeleteMessage",                       // deletes messages
	"DeleteConversation",                  // deletes chats
	"UpdateConversation",                  // archive, mute, rename, delete
	"SetTyping",                           // sends typing indicators
	"UpdateSettings",                      // changes Messages settings
	"RegisterPush",                        // registers push and changes settings
	"SetActiveSession",                    // takes over from other web sessions
	"Unpair", "UnpairBugle", "UnpairGaia", // revoke pairing; done on the phone instead
	"StartUploadMedia", "FinalizeUploadMedia", // only via UploadMedia in write.go
)

// bannedUnneeded are reads or plumbing gm-reader has no use for. Keeping them
// off keeps the surface small (and media is never downloaded).
var bannedUnneeded = set(
	"DoGaiaPairing", "StartLogin", "GenerateQRCodeData", "RegisterPhoneRelay", "RefreshPhoneRelay",
	"GetWebEncryptionKey", "Reconnect", "ConnectBackground", "HandleRPCMsg",
	"SetPingInterval", "SetAlertTimeoutCount", "SetDataReceiveCheckInterval",
	"DownloadMedia", "DownloadAvatar", "GetFullSizeImage", "GetParticipantThumbnail", "GetContactThumbnail",
	"ListTopContacts", "GetConversationType", "IsBugleDefault", "HasOtherGaiaPhone",
)

// unsafeAllowed lists the only files that may import "unsafe" (Windows API calls).
var unsafeAllowed = set(
	"internal/secure/secure_windows.go",
	"internal/confirm/dialog_windows.go",
	"internal/clipboard/clipboard_windows.go",
)

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func libgmMethods(t *testing.T) map[string]bool {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", libgmModule).Output()
	if err == nil && strings.TrimSpace(string(out)) == "" {
		// Not in the module cache yet: download it and ask again.
		_ = exec.Command("go", "mod", "download", libgmModule).Run()
		out, err = exec.Command("go", "list", "-m", "-f", "{{.Dir}}", libgmModule).Output()
	}
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("cannot locate libgm source: %v", err)
	}
	dir := filepath.Join(strings.TrimSpace(string(out)), "pkg", "libgm")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no libgm source files in %s", dir)
	}
	re := regexp.MustCompile(`(?m)^func \([a-zA-Z_]+ \*Client\) ([A-Z][A-Za-z0-9_]*)\(`)
	methods := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			methods[string(m[1])] = true
		}
	}
	if len(methods) < 20 {
		t.Fatalf("found only %d libgm methods; parser is broken", len(methods))
	}
	return methods
}

func TestEveryLibgmMethodIsClassified(t *testing.T) {
	methods := libgmMethods(t)
	var unclassified, stale []string
	for m := range methods {
		if !readAllowed[m] && !writeAllowed[m] && !bannedStateChanging[m] && !bannedUnneeded[m] {
			unclassified = append(unclassified, m)
		}
	}
	for _, group := range []map[string]bool{readAllowed, writeAllowed, bannedStateChanging, bannedUnneeded} {
		for m := range group {
			if !methods[m] {
				stale = append(stale, m)
			}
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("libgm has methods this guard does not classify; review and add them: %v", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("guard lists methods libgm no longer has; libgm changed, review the guard: %v", stale)
	}
}

func TestLibgmUsage(t *testing.T) {
	root := repoRoot(t)
	methods := libgmMethods(t)
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "testdata" || name == "dist" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		checked++
		inGM := strings.HasPrefix(rel, "internal/gm/")
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, libgmModule) && !inGM {
				t.Errorf("%s imports %s; only internal/gm may use libgm", rel, p)
			}
			if p == "reflect" {
				t.Errorf("%s imports reflect, which is not allowed", rel)
			}
			if p == "unsafe" && !unsafeAllowed[rel] {
				t.Errorf("%s imports unsafe, which is only allowed for the Windows API files", rel)
			}
		}
		if inGM && !strings.HasSuffix(rel, "_test.go") {
			checkNoExportedLibgmTypes(t, fset, rel, f)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			if !methods[name] {
				return true
			}
			pos := fset.Position(sel.Pos())
			switch {
			case bannedStateChanging[name] || bannedUnneeded[name]:
				t.Errorf("%s:%d uses banned libgm method %s", rel, pos.Line, name)
			case writeAllowed[name] && rel != "internal/gm/write.go":
				t.Errorf("%s:%d uses write method %s outside internal/gm/write.go", rel, pos.Line, name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("only checked %d files; the walk is broken", checked)
	}
}

// libgmPkgs are the import names of libgm packages inside internal/gm.
var libgmPkgs = set("libgm", "gmproto", "events")

func mentionsLibgm(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && libgmPkgs[id.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

// checkNoExportedLibgmTypes fails if an exported function, method, variable,
// struct field or type in internal/gm uses a libgm type.
func checkNoExportedLibgmTypes(t *testing.T, fset *token.FileSet, rel string, f *ast.File) {
	report := func(pos token.Pos, what string) {
		t.Errorf("%s:%d exports %s with a libgm type; internal/gm must only expose its own types", rel, fset.Position(pos).Line, what)
	}
	checkFields := func(fl *ast.FieldList, what string, onlyExported bool) {
		if fl == nil {
			return
		}
		for _, fld := range fl.List {
			exported := !onlyExported
			for _, n := range fld.Names {
				if n.IsExported() {
					exported = true
				}
			}
			if onlyExported && len(fld.Names) == 0 {
				exported = true // embedded field
			}
			if exported && mentionsLibgm(fld.Type) {
				report(fld.Pos(), what)
			}
		}
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recv := d.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok && !id.IsExported() {
					continue
				}
			}
			checkFields(d.Type.Params, "function "+d.Name.Name, false)
			checkFields(d.Type.Results, "function "+d.Name.Name, false)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					if !sp.Name.IsExported() {
						continue
					}
					if st, ok := sp.Type.(*ast.StructType); ok {
						checkFields(st.Fields, "struct "+sp.Name.Name, true)
					} else if it, ok := sp.Type.(*ast.InterfaceType); ok {
						checkFields(it.Methods, "interface "+sp.Name.Name, false)
					} else if mentionsLibgm(sp.Type) {
						report(sp.Pos(), "type "+sp.Name.Name)
					}
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						if n.IsExported() && sp.Type != nil && mentionsLibgm(sp.Type) {
							report(sp.Pos(), "variable "+n.Name)
						}
					}
				}
			}
		}
	}
}
