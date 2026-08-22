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
