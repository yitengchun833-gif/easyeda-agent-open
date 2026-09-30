package app

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func identityArchive(t *testing.T, method uint16, names []string, source string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, name := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(source)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func runIdentityJavaScript(t *testing.T, code string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("browser identity compatibility script verification requires Node.js")
	}
	if err := exec.Command(node, "-e", `new DecompressionStream("deflate-raw")`).Run(); err != nil {
		t.Skip("browser identity compatibility script verification requires deflate-raw DecompressionStream")
	}
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(code)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("identity probe script failed: %s: %v", out, err)
	}
	return out
}

func identityArchiveSource() string {
	entry := identityCompatNativeFootprint()
	return `{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"page-1"}|` + "\n" + strings.Join(entry.MetadataLines, "\n") + "\n" +
		`{"type":"DOCHEAD"}||{"docType":"FOOTPRINT","uuid":"other-instance"}|` + "\n" +
		`{"type":"META"}||{"source":"must-not-be-attached-to-previous-footprint"}|`
}

func TestSchematicIdentityCompatOfficialArchiveBounds(t *testing.T) {
	for _, scenario := range []string{"stored", "deflated", "nested-epru", "duplicate-epru", "missing-epru", "missing-page", "duplicate-page", "bad-json", "bad-crc", "local-name-mismatch", "unsupported-method", "encrypted", "multidisk", "zip64", "bad-directory-bounds", "oversized-entry", "wrong-expanded-length", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			source, names, method := identityArchiveSource(), []string{"工程.epru"}, uint16(zip.Store)
			switch scenario {
			case "deflated":
				method = zip.Deflate
			case "nested-epru":
				names = []string{"nested/project.epru"}
			case "duplicate-epru":
				names = []string{"a.epru", "b.epru"}
			case "missing-epru":
				names = []string{"project.json"}
			case "missing-page":
				source = strings.ReplaceAll(source, "page-1", "different-page")
			case "duplicate-page":
				source += "\n" + `{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"page-1"}|`
			case "bad-json":
				source = `{"type":"DOCHEAD"}||broken|`
			}
			data := identityArchive(t, method, names, source)
			central := bytes.Index(data, []byte{'P', 'K', 1, 2})
			end := len(data) - 22
			switch scenario {
			case "bad-crc":
				data[central+16] ^= 1
			case "local-name-mismatch":
				data[30] ^= 1
			case "unsupported-method":
				binary.LittleEndian.PutUint16(data[central+10:], 9)
			case "encrypted":
				data[central+8] |= 1
			case "multidisk":
				data[end+4] = 1
			case "zip64":
				binary.LittleEndian.PutUint16(data[end+8:], 65535)
				binary.LittleEndian.PutUint16(data[end+10:], 65535)
			case "bad-directory-bounds":
				data[end+12] ^= 1
			case "oversized-entry":
				binary.LittleEndian.PutUint32(data[central+24:], 64*1024*1024+1)
			case "wrong-expanded-length":
				binary.LittleEndian.PutUint32(data[central+24:], 1)
			case "truncated":
				data = data[:len(data)-20]
			}
			code := schematicIdentityArchiveCode + `
(async () => {
 try {
  const file = new Blob([Buffer.from("` + base64.StdEncoding.EncodeToString(data) + `", "base64")]);
  const entries = await identityFootprintsFromProject(file, new Set(["abe23dba1def1246"]), "page-1");
  process.stdout.write(JSON.stringify({entries}));
 } catch(error) { process.stdout.write(JSON.stringify({error:String(error)})); }
})();`
			var result struct {
				Entries []schematicNativeFootprint `json:"entries"`
				Error   string                     `json:"error"`
			}
			if err := json.Unmarshal(runIdentityJavaScript(t, code), &result); err != nil {
				t.Fatal(err)
			}
			if scenario != "stored" && scenario != "deflated" {
				if result.Error == "" {
					t.Fatal("malformed or unbound project archive accepted")
				}
				return
			}
			if result.Error != "" || len(result.Entries) != 1 || result.Entries[0].SourceKind != "project-epro2" {
				t.Fatalf("official archive not resolved: %+v", result)
			}
			asset, err := schematicNativeFootprintIdentity("abe23dba1def1246", result.Entries)
			if err != nil || asset.UUID != identityCompatSource().UUID || asset.LibraryUUID != "system" {
				t.Fatalf("selected provenance owner/source changed: %+v %v", asset, err)
			}
		})
	}
}

func TestSchematicIdentityCompatProbeUsesFreshOfficialArchive(t *testing.T) {
	data := identityArchive(t, zip.Deflate, []string{"工程.epru"}, identityArchiveSource())
	code, _ := json.Marshal(schematicIdentityProbeCode([]string{"C6186"}, []string{"abe23dba1def1246"}, "project-1", "page-1"))
	for _, scenario := range []string{"empty-sources", "source-api-fails", "already-has-source", "wrong-page", "context-drift"} {
		t.Run(scenario, func(t *testing.T) {
			scenarioJSON, _ := json.Marshal(scenario)
			entry, _ := json.Marshal(identityCompatNativeFootprint())
			js := `
const scenario = ` + string(scenarioJSON) + `, entry = ` + string(entry) + `;
let exports = 0, contextReads = 0;
const eda = {
 dmt_Project:{getCurrentProjectInfo:async()=>({uuid:"project-1"})},
 dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:(scenario === "wrong-page" || scenario === "context-drift" && ++contextReads > 2) ? "wrong-page" : "page-1"})},
 sys_FileManager:{
  getDocumentFootprintSources:async()=>{if(scenario === "source-api-fails")throw new Error("unavailable");return scenario === "already-has-source" ? [{footprintUuid:entry.footprintUuid,documentSource:entry.metadataLines.join("\n")}] : [];},
  getProjectFile:async(name,unused,type)=>{if(type !== "epro2")throw new Error("wrong export format");exports++;return new Blob([Buffer.from("` + base64.StdEncoding.EncodeToString(data) + `","base64")]);}
 },
 lib_Device:{getByLcscIds:async(ids,unused,all)=>{if(all !== true)throw new Error("incomplete lookup");return [];} }
};
(async()=>{try {const value=await new (Object.getPrototypeOf(async function(){}).constructor)("eda",` + string(code) + `)(eda);process.stdout.write(JSON.stringify({value,exports}));}catch(error){process.stdout.write(JSON.stringify({error:String(error),exports}));}})();`
			var result struct {
				Value   schematicIdentityProbe `json:"value"`
				Error   string                 `json:"error"`
				Exports int                    `json:"exports"`
			}
			if err := json.Unmarshal(runIdentityJavaScript(t, js), &result); err != nil {
				t.Fatal(err)
			}
			if scenario == "wrong-page" || scenario == "context-drift" {
				if result.Error == "" {
					t.Fatal("probe accepted context drift")
				}
				return
			}
			if result.Error != "" || result.Value.SourceError != "" || len(result.Value.NativeFootprints) != 1 {
				t.Fatalf("fallback failed: %+v", result)
			}
			wantExports := 1
			if scenario == "already-has-source" {
				wantExports = 0
			}
			if result.Exports != wantExports {
				t.Fatalf("exports=%d, want %d", result.Exports, wantExports)
			}
		})
	}
}
