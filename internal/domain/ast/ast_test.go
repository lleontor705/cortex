package ast

import (
	"os"
	"path/filepath"
	"strings"
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

func TestExtractDotNetFiles(t *testing.T) {
	tempDir := t.TempDir()

	csCode := `using System;
using System.Collections.Generic;

namespace Cortex.Enterprise;

public interface IGraphService {
    void Build();
}

public struct GraphNode {
    public string Id;
}

public record GraphRecord(string Name);

public enum GraphStatus {
    Ready,
    Building
}

public class MemoryGraph : IGraphService {
    public void Build() {
        Console.WriteLine("Built");
    }
}
`
	csPath := filepath.Join(tempDir, "MemoryGraph.cs")
	if err := os.WriteFile(csPath, []byte(csCode), 0644); err != nil {
		t.Fatalf("failed to write C# file: %v", err)
	}

	fsCode := `open System
module GraphAnalytics =
    type MetricRecord = { Name: string }
    let computeMetric x = x + 1
`
	fsPath := filepath.Join(tempDir, "Analytics.fs")
	if err := os.WriteFile(fsPath, []byte(fsCode), 0644); err != nil {
		t.Fatalf("failed to write F# file: %v", err)
	}

	vbCode := `Imports System
Namespace EnterpriseApp
    Public Interface IStorage
    End Interface

    Public Class LocalStorage
        Public Sub Save()
        End Sub
    End Class
End Namespace
`
	vbPath := filepath.Join(tempDir, "Storage.vb")
	if err := os.WriteFile(vbPath, []byte(vbCode), 0644); err != nil {
		t.Fatalf("failed to write VB file: %v", err)
	}

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 50)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if res.FilesScanned < 3 {
		t.Fatalf("expected 3 files scanned, got %d", res.FilesScanned)
	}

	foundCsClass := false
	foundCsInterface := false
	foundCsStruct := false
	foundCsRecord := false
	foundCsEnum := false
	foundFsModule := false
	foundFsType := false
	foundFsFunc := false
	foundVbClass := false
	foundVbInterface := false

	for _, ent := range res.Entities {
		switch ent.Kind {
		case "class":
			if ent.Name == "MemoryGraph" {
				foundCsClass = true
			}
			if ent.Name == "LocalStorage" {
				foundVbClass = true
			}
		case "interface":
			if ent.Name == "IGraphService" {
				foundCsInterface = true
			}
			if ent.Name == "IStorage" {
				foundVbInterface = true
			}
		case "struct":
			if ent.Name == "GraphNode" {
				foundCsStruct = true
			}
			if ent.Name == "MetricRecord" {
				foundFsType = true
			}
		case "record":
			if ent.Name == "GraphRecord" {
				foundCsRecord = true
			}
		case "enum":
			if ent.Name == "GraphStatus" {
				foundCsEnum = true
			}
		case "module":
			if ent.Name == "GraphAnalytics" {
				foundFsModule = true
			}
		case "func":
			if ent.Name == "computeMetric" {
				foundFsFunc = true
			}
		}
	}

	if !foundCsClass || !foundCsInterface || !foundCsStruct || !foundCsRecord || !foundCsEnum {
		t.Errorf("C# symbol extraction failed: class=%v, if=%v, struct=%v, record=%v, enum=%v",
			foundCsClass, foundCsInterface, foundCsStruct, foundCsRecord, foundCsEnum)
	}
	if !foundFsModule || !foundFsType || !foundFsFunc {
		t.Errorf("F# symbol extraction failed: module=%v, type=%v, func=%v",
			foundFsModule, foundFsType, foundFsFunc)
	}
	if !foundVbClass || !foundVbInterface {
		t.Errorf("VB.NET symbol extraction failed: class=%v, if=%v",
			foundVbClass, foundVbInterface)
	}
}

func TestExtractJVMFiles(t *testing.T) {
	tempDir := t.TempDir()

	javaCode := `package com.cortex.core;

import java.util.List;

public interface Repository {
    void find();
}

public record Item(String id) {}

public enum State { ON, OFF }

public class Service implements Repository {
    public void find() {}
}
`
	javaPath := filepath.Join(tempDir, "Service.java")
	_ = os.WriteFile(javaPath, []byte(javaCode), 0644)

	ktCode := `package com.cortex.kt

import com.cortex.core.*

interface Handler

data class User(val name: String)

object AppRegistry

class Controller : Handler {
    fun process() {}
}
`
	ktPath := filepath.Join(tempDir, "Controller.kt")
	_ = os.WriteFile(ktPath, []byte(ktCode), 0644)

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 50)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if res.FilesScanned < 2 {
		t.Fatalf("expected 2 files scanned, got %d", res.FilesScanned)
	}

	foundJavaClass := false
	foundJavaInterface := false
	foundJavaRecord := false
	foundJavaEnum := false
	foundKtClass := false
	foundKtInterface := false
	foundKtObject := false
	foundKtFun := false

	for _, ent := range res.Entities {
		if ent.Name == "Service" && ent.Kind == "class" {
			foundJavaClass = true
		}
		if ent.Name == "Repository" && ent.Kind == "interface" {
			foundJavaInterface = true
		}
		if ent.Name == "Item" && ent.Kind == "record" {
			foundJavaRecord = true
		}
		if ent.Name == "State" && ent.Kind == "enum" {
			foundJavaEnum = true
		}
		if ent.Name == "User" && ent.Kind == "class" {
			foundKtClass = true
		}
		if ent.Name == "Handler" && ent.Kind == "interface" {
			foundKtInterface = true
		}
		if ent.Name == "AppRegistry" && ent.Kind == "class" {
			foundKtObject = true
		}
		if ent.Name == "process" && ent.Kind == "func" {
			foundKtFun = true
		}
	}

	if !foundJavaClass || !foundJavaInterface || !foundJavaRecord || !foundJavaEnum {
		t.Errorf("Java symbol extraction incomplete: class=%v, if=%v, rec=%v, enum=%v",
			foundJavaClass, foundJavaInterface, foundJavaRecord, foundJavaEnum)
	}
	if !foundKtClass || !foundKtInterface || !foundKtObject || !foundKtFun {
		t.Errorf("Kotlin symbol extraction incomplete: class=%v, if=%v, obj=%v, fun=%v",
			foundKtClass, foundKtInterface, foundKtObject, foundKtFun)
	}
}

func TestExtractNativeFiles(t *testing.T) {
	tempDir := t.TempDir()

	rsCode := `use std::sync::Arc;
pub mod internal;

pub trait Engine {
    fn run(&self);
}

pub struct CoreEngine {
    id: String,
}

pub enum EngineMode {
    Fast,
    Safe,
}

pub fn start() {}
`
	rsPath := filepath.Join(tempDir, "lib.rs")
	_ = os.WriteFile(rsPath, []byte(rsCode), 0644)

	cppCode := `#include <iostream>
namespace CortexEngine {
    class Pipeline {
    public:
        void Execute();
    };
    struct Config {
        int timeout;
    };
    void Initialize() {}
}
`
	cppPath := filepath.Join(tempDir, "pipeline.cpp")
	_ = os.WriteFile(cppPath, []byte(cppCode), 0644)

	phpCode := `<?php
namespace App\Services;
use App\Models\User;

interface Notifier {
    public function notify();
}

class AlertService implements Notifier {
    public function notify() {}
}
`
	phpPath := filepath.Join(tempDir, "AlertService.php")
	_ = os.WriteFile(phpPath, []byte(phpCode), 0644)

	rbCode := `require 'json'
module Analytics
  class Tracker
    def track_event
    end
  end
end
`
	rbPath := filepath.Join(tempDir, "tracker.rb")
	_ = os.WriteFile(rbPath, []byte(rbCode), 0644)

	swiftCode := `import Foundation

public protocol Worker {
    func execute()
}

public class BackgroundTask: Worker {
    public func execute() {}
}
`
	swiftPath := filepath.Join(tempDir, "task.swift")
	_ = os.WriteFile(swiftPath, []byte(swiftCode), 0644)

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 50)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if res.FilesScanned < 5 {
		t.Fatalf("expected 5 files scanned, got %d", res.FilesScanned)
	}

	foundRsStruct := false
	foundRsTrait := false
	foundCppClass := false
	foundPhpClass := false
	foundRbClass := false
	foundSwiftProto := false
	foundSwiftClass := false

	for _, ent := range res.Entities {
		if ent.Name == "CoreEngine" && ent.Kind == "struct" {
			foundRsStruct = true
		}
		if ent.Name == "Engine" && ent.Kind == "interface" {
			foundRsTrait = true
		}
		if ent.Name == "Pipeline" && ent.Kind == "class" {
			foundCppClass = true
		}
		if ent.Name == "AlertService" && ent.Kind == "class" {
			foundPhpClass = true
		}
		if ent.Name == "Tracker" && ent.Kind == "class" {
			foundRbClass = true
		}
		if ent.Name == "Worker" && ent.Kind == "interface" {
			foundSwiftProto = true
		}
		if ent.Name == "BackgroundTask" && ent.Kind == "class" {
			foundSwiftClass = true
		}
	}

	if !foundRsStruct || !foundRsTrait {
		t.Errorf("Rust extraction failed: struct=%v, trait=%v", foundRsStruct, foundRsTrait)
	}
	if !foundCppClass {
		t.Errorf("C++ extraction failed: class=%v", foundCppClass)
	}
	if !foundPhpClass {
		t.Errorf("PHP extraction failed: class=%v", foundPhpClass)
	}
	if !foundRbClass {
		t.Errorf("Ruby extraction failed: class=%v", foundRbClass)
	}
	if !foundSwiftProto || !foundSwiftClass {
		t.Errorf("Swift extraction failed: proto=%v, class=%v", foundSwiftProto, foundSwiftClass)
	}
}

func TestExtractDirIgnoresArtifactFolders(t *testing.T) {
	tempDir := t.TempDir()

	binDir := filepath.Join(tempDir, "bin", "Debug", "net8.0")
	_ = os.MkdirAll(binDir, 0755)
	_ = os.WriteFile(filepath.Join(binDir, "Ignored.cs"), []byte("class Ignored {}"), 0644)

	targetDir := filepath.Join(tempDir, "target", "debug")
	_ = os.MkdirAll(targetDir, 0755)
	_ = os.WriteFile(filepath.Join(targetDir, "Ignored.rs"), []byte("struct Ignored;"), 0644)

	srcDir := filepath.Join(tempDir, "src")
	_ = os.MkdirAll(srcDir, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "Real.cs"), []byte("public class RealApp {}"), 0644)

	extractor := NewExtractor(tempDir)
	res, err := extractor.ExtractDir(tempDir, 50)
	if err != nil {
		t.Fatalf("ExtractDir failed: %v", err)
	}

	if res.FilesScanned != 1 {
		t.Fatalf("expected exactly 1 file scanned, got %d", res.FilesScanned)
	}

	for _, ent := range res.Entities {
		if ent.Name == "Ignored" {
			t.Errorf("expected artifact files to be ignored, found %s", ent.Name)
		}
	}
}

func TestHighDensityGoExtraction(t *testing.T) {
	tempDir := t.TempDir()
	goCode := `package engine

// EngineService manages graph compute operations.
type EngineService struct {
	ID   string ` + "`json:\"id\"`" + `
	Host string ` + "`json:\"host\"`" + `
}

// CalculateMetrics computes complex graph analytics.
func (s *EngineService) CalculateMetrics(depth int, filter string) (int, error) {
	if depth > 10 {
		return 0, nil
	} else if depth > 5 {
		return 5, nil
	}
	return depth, nil
}
`
	goPath := filepath.Join(tempDir, "service.go")
	if err := os.WriteFile(goPath, []byte(goCode), 0644); err != nil {
		t.Fatalf("failed to write go file: %v", err)
	}

	extractor := NewExtractor(tempDir)
	graph, err := extractor.ExtractCodeGraph(tempDir, "testproj", 10)
	if err != nil {
		t.Fatalf("ExtractCodeGraph failed: %v", err)
	}

	foundStruct := false
	foundMethod := false

	for _, sym := range graph.Symbols {
		if sym.Name == "EngineService" && sym.Kind == "struct" {
			foundStruct = true
			if !strings.Contains(sym.DocSummary, "EngineService manages graph compute") {
				t.Errorf("expected doc summary, got %q", sym.DocSummary)
			}
			if fields, ok := sym.Metadata["fields"].([]map[string]string); ok {
				if len(fields) != 2 {
					t.Errorf("expected 2 struct fields, got %d", len(fields))
				}
			} else {
				t.Errorf("expected metadata.fields on struct, got nil")
			}
		}

		if sym.Name == "CalculateMetrics" && sym.Kind == "method" {
			foundMethod = true
			if sym.Complexity < 3 {
				t.Errorf("expected complexity >= 3, got %d", sym.Complexity)
			}
			if len(sym.Parameters) != 2 {
				t.Fatalf("expected 2 parameters, got %d", len(sym.Parameters))
			}
			if sym.Parameters[0].Name != "depth" || sym.Parameters[0].Type != "int" {
				t.Errorf("unexpected param 0: %+v", sym.Parameters[0])
			}
			if sym.ReturnType != "(int, error)" {
				t.Errorf("expected return type (int, error), got %q", sym.ReturnType)
			}
			if !strings.Contains(sym.DocSummary, "CalculateMetrics computes complex graph") {
				t.Errorf("expected method doc summary, got %q", sym.DocSummary)
			}
		}
	}

	if !foundStruct {
		t.Errorf("struct EngineService not found")
	}
	if !foundMethod {
		t.Errorf("method CalculateMetrics not found")
	}
}

func TestHighDensityTSAndPython(t *testing.T) {
	tempDir := t.TempDir()

	tsCode := `
/**
 * UserService provides user lifecycle operations.
 */
export class UserService extends BaseService implements IAuthService {
  /**
   * Authenticates a user with token.
   */
  public async authenticate(token: string, retries: number): Promise<boolean> {
    return true;
  }
}
`
	tsPath := filepath.Join(tempDir, "user.ts")
	_ = os.WriteFile(tsPath, []byte(tsCode), 0644)

	pyCode := `
class TaskWorker(BaseWorker):
    """Worker handling asynchronous background tasks."""

    @dataclass
    def execute(self, task_id: str, timeout: int = 30) -> bool:
        """Executes a single unit of work."""
        return True
`
	pyPath := filepath.Join(tempDir, "worker.py")
	_ = os.WriteFile(pyPath, []byte(pyCode), 0644)

	extractor := NewExtractor(tempDir)
	graph, err := extractor.ExtractCodeGraph(tempDir, "testproj", 10)
	if err != nil {
		t.Fatalf("ExtractCodeGraph failed: %v", err)
	}

	foundTSClass := false
	foundTSMethod := false
	foundPyClass := false
	foundPyMethod := false

	for _, sym := range graph.Symbols {
		if sym.Name == "UserService" && sym.Kind == "class" {
			foundTSClass = true
			if !strings.Contains(sym.DocSummary, "UserService provides user lifecycle") {
				t.Errorf("expected TS JSDoc, got %q", sym.DocSummary)
			}
		}
		if sym.Name == "authenticate" && sym.Kind == "method" {
			foundTSMethod = true
			if len(sym.Parameters) != 2 {
				t.Errorf("expected 2 params for TS method, got %d", len(sym.Parameters))
			}
			if sym.ReturnType != "Promise<boolean>" {
				t.Errorf("expected Promise<boolean>, got %q", sym.ReturnType)
			}
		}
		if sym.Name == "TaskWorker" && sym.Kind == "class" {
			foundPyClass = true
			if !strings.Contains(sym.DocSummary, "Worker handling asynchronous") {
				t.Errorf("expected Python class docstring, got %q", sym.DocSummary)
			}
		}
		if sym.Name == "execute" && sym.Kind == "method" {
			foundPyMethod = true
			if len(sym.Parameters) != 2 {
				t.Errorf("expected 2 params for Python method, got %d", len(sym.Parameters))
			}
			if sym.ReturnType != "bool" {
				t.Errorf("expected return type bool, got %q", sym.ReturnType)
			}
		}
	}

	if !foundTSClass || !foundTSMethod {
		t.Errorf("TS class or method missing")
	}
	if !foundPyClass || !foundPyMethod {
		t.Errorf("Python class or method missing")
	}
}

func TestSQLExtractionAndForeignKeyRelations(t *testing.T) {
	tempDir := t.TempDir()
	sqlCode := `
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    email VARCHAR(255) UNIQUE
);

CREATE TABLE orders (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL REFERENCES users(id),
    total_amount DECIMAL(10,2)
);
`
	sqlPath := filepath.Join(tempDir, "schema.sql")
	_ = os.WriteFile(sqlPath, []byte(sqlCode), 0644)

	extractor := NewExtractor(tempDir)
	graph, err := extractor.ExtractCodeGraph(tempDir, "testproj", 10)
	if err != nil {
		t.Fatalf("ExtractCodeGraph failed: %v", err)
	}

	foundUsersTable := false
	foundOrdersTable := false
	foundFKRelation := false

	for _, sym := range graph.Symbols {
		if sym.Name == "users" && sym.Kind == "table" {
			foundUsersTable = true
			if len(sym.Parameters) != 3 {
				t.Errorf("expected 3 columns for users table, got %d", len(sym.Parameters))
			}
		}
		if sym.Name == "orders" && sym.Kind == "table" {
			foundOrdersTable = true
		}
	}

	for _, rel := range graph.Relations {
		if rel.Relation == "references" {
			foundFKRelation = true
		}
	}

	if !foundUsersTable || !foundOrdersTable {
		t.Errorf("SQL tables extraction failed")
	}
	if !foundFKRelation {
		t.Errorf("expected foreign key 'references' relation between orders and users")
	}
}

func TestCrossFileTwoPassResolution(t *testing.T) {
	tempDir := t.TempDir()

	utilsCode := `
export function computeHash(input: string): string {
  return "hash_" + input;
}
`
	utilsPath := filepath.Join(tempDir, "utils.ts")
	_ = os.WriteFile(utilsPath, []byte(utilsCode), 0644)

	appCode := `
import { computeHash } from "./utils";

export function runApp() {
  const h = computeHash("data");
}
`
	appPath := filepath.Join(tempDir, "app.ts")
	_ = os.WriteFile(appPath, []byte(appCode), 0644)

	extractor := NewExtractor(tempDir)
	graph, err := extractor.ExtractCodeGraph(tempDir, "testproj", 10)
	if err != nil {
		t.Fatalf("ExtractCodeGraph failed: %v", err)
	}

	var computeHashID, runAppID string
	for _, sym := range graph.Symbols {
		if sym.Name == "computeHash" {
			computeHashID = sym.ID
		}
		if sym.Name == "runApp" {
			runAppID = sym.ID
		}
	}

	if computeHashID == "" || runAppID == "" {
		t.Fatalf("expected symbols computeHash and runApp to be indexed")
	}

	foundResolvedCall := false
	for _, rel := range graph.Relations {
		if rel.SourceID == runAppID && rel.TargetID == computeHashID && rel.Relation == "calls" {
			foundResolvedCall = true
			break
		}
	}

	if !foundResolvedCall {
		t.Errorf("expected 2-pass resolver to connect runApp -> computeHash with 'calls' relation")
	}
}
