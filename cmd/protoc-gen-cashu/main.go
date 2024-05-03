package main

import (
	"flag"
	"fmt"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
)

const version = "v1.0.0"

func main() {
	var flags flag.FlagSet
	protogen.Options{
		ParamFunc: flags.Set,
	}.Run(func(gen *protogen.Plugin) error {
		for _, file := range gen.Files {
			if !file.Generate {
				continue
			}
			generateFile(gen, file)
			//generatePythonFile(gen, file)˜◊

		}
		return nil
	})
	fmt.Fprintln(os.Stderr, "protoc-gen-helloworld: version", version)
}

func generateFile(gen *protogen.Plugin, file *protogen.File) {
	filename := file.GeneratedFilenamePrefix + ".pb.cashu.go"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)
	g.P("// Code generated by protoc-gen-cashu. DO NOT EDIT.")
	g.P("package ", file.GoPackageName)
	g.P()

	// Add the imports here
	g.P("import (")
	g.P("\"encoding/base64\"")
	g.P("\"encoding/json\"")
	g.P(")")
	g.P()

	for _, message := range file.Messages {
		generateMethodsForMessage(g, message)
	}
}

func generatePythonFile(gen *protogen.Plugin, file *protogen.File) {
	filename := file.GeneratedFilenamePrefix + ".pb.cashu.py"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)
	g.P("# Code generated by protoc-gen-cashu. DO NOT EDIT.")
	g.P("import base64")
	g.P("import json")

	g.P()
	for _, message := range file.Messages {
		generateMethodsForMessagePython(g, message)
	}

}

func generateMethodsForMessage(g *protogen.GeneratedFile, message *protogen.Message) {
	if message.GoIdent.GoName == "TokenV3" {
		// Add ToString() method
		g.P("func (x *", message.GoIdent, ") ToString() string {")
		g.P("  jsonBytes, err := json.Marshal(x)")
		g.P("  if err != nil {")
		g.P("    panic(err)")
		g.P("  }")
		g.P("  token := base64.URLEncoding.EncodeToString(jsonBytes)")
		g.P("  return \"cashuA\" + token")
		g.P("}")

		// Add TotalAmount() method
		g.P("func (t *", message.GoIdent, ") TotalAmount() uint64 {")
		g.P("  var totalAmount uint64 = 0")
		g.P("  for _, tokenProof := range t.Token {")
		g.P("    for _, proof := range tokenProof.Proofs.Proofs {")
		g.P("      totalAmount += proof.Amount")
		g.P("    }")
		g.P("  }")
		g.P("  return totalAmount")
		g.P("}")
	}
}

func generateMethodsForMessagePython(g *protogen.GeneratedFile, message *protogen.Message) {
	if message.GoIdent.GoName == "TokenV3" {
		// Add ToString() method
		g.P("def to_string(self):")
		g.P("  json_bytes = json.dumps(self.__dict__)")
		g.P("  token = base64.urlsafe_b64encode(json_bytes.encode())")
		g.P("  return \"cashuA\" + token.decode()")

		g.P("def total_amount(self):")
		g.P("  total_amount = 0")
		g.P("  for token_proof in self.Token:")
		g.P("    for proof in token_proof.Proofs.Proofs:")
		g.P("      total_amount += proof.Amount")
		g.P("  return total_amount")
	}
}