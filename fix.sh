#!/bin/bash
sed -i '' 's/"regexp"//' internal/compiler/disassembler_test.go
sed -i '' 's/"github.com\/benhoyt\/goawk\/internal\/resolver"/"regexp"\
	"github.com\/benhoyt\/goawk\/internal\/resolver"\
	"github.com\/benhoyt\/goawk\/regex"/' internal/compiler/disassembler_test.go

sed -i '' 's/prog.Compiled, err = compiler.Compile(&prog.ResolvedProgram)/var compConfig *compiler.Config\
	if config != nil \&\& config.RegexCompiler != nil {\
		compConfig = \&compiler.Config{RegexCompiler: config.RegexCompiler}\
	}\
	prog.Compiled, err = compiler.Compile(\&prog.ResolvedProgram, compConfig)/' goawk.go
