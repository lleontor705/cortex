package ast

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractGoFile(t *testing.T) {
	tempDir := t.TempDir()
	goCode := `package sample

import "fmt"

type Service struct {
	Name string
}

type Greeter interface {
	Greet() string
}

func (s *Service) Greet() string {
	fmt.Println("Hello")
	return s.Name
}

func Helper() {
	s := &Service{Name: "Cortex"}
	s.Greet()
}
`
	goFilePath := filepath.Join(tempDir, "sample.go")
	if err := os.WriteFile(goFilePath, []byte(goCode), 0644); err != nil {
		t.Fatalf("failed to write test go file: %v", err)
	}

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 10)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if len(res.Entities) == 0 {
		t.Fatalf("expected entities, got 0")
	}

	foundStruct := false
	foundInterface := false
	foundMethod := false
	foundFunc := false

	for _, ent := range res.Entities {
		switch ent.Kind {
		case "struct":
			if ent.Name == "Service" {
				foundStruct = true
			}
		case "interface":
			if ent.Name == "Greeter" {
				foundInterface = true
			}
		case "method":
			if ent.Name == "Greet" {
				foundMethod = true
			}
		case "func":
			if ent.Name == "Helper" {
				foundFunc = true
			}
		}
	}

	if !foundStruct {
		t.Errorf("expected Service struct to be extracted")
	}
	if !foundInterface {
		t.Errorf("expected Greeter interface to be extracted")
	}
	if !foundMethod {
		t.Errorf("expected Greet method to be extracted")
	}
	if !foundFunc {
		t.Errorf("expected Helper func to be extracted")
	}
}

func TestExtractTSAndPythonFiles(t *testing.T) {
	tempDir := t.TempDir()

	tsCode := `import { useState } from "react";
export class AgentService {
  execute() {}
}
export function runTask() {}
`
	tsFilePath := filepath.Join(tempDir, "agent.ts")
	_ = os.WriteFile(tsFilePath, []byte(tsCode), 0644)

	pyCode := `import os
from cortex import client

class KnowledgeGraph:
    def build(self):
        pass

def process_memory():
    pass
`
	pyFilePath := filepath.Join(tempDir, "graph.py")
	_ = os.WriteFile(pyFilePath, []byte(pyCode), 0644)

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 10)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if res.FilesScanned < 2 {
		t.Errorf("expected at least 2 files scanned, got %d", res.FilesScanned)
	}
}

func TestExtractSingleFileAndPath(t *testing.T) {
	tempDir := t.TempDir()
	goCode := `package refactor
func OldFunc() {}
func NewFunc() { OldFunc() }
`
	goFilePath := filepath.Join(tempDir, "refactor.go")
	_ = os.WriteFile(goFilePath, []byte(goCode), 0644)

	extractor := NewExtractor(tempDir)
	resFile, err := extractor.ExtractFile(goFilePath)
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}
	if len(resFile.Entities) != 3 {
		t.Errorf("expected 3 entities (module + 2 funcs), got %d", len(resFile.Entities))
	}

	resPath, err := extractor.ExtractPath(goFilePath, 10)
	if err != nil {
		t.Fatalf("ExtractPath on single file failed: %v", err)
	}
	if len(resPath.Entities) != 3 {
		t.Errorf("expected 3 entities from ExtractPath, got %d", len(resPath.Entities))
	}
}
