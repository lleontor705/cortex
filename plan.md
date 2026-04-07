1. **Identify the Bottleneck**: The file `internal/mcp/tools_memory.go` uses `regexp.MustCompile` inside function calls. Specifically, in `normalizeTopicSegment` it compiles `regexp.MustCompile(`[^a-z0-9]+`)` every time it's called. Also in `cleanMarkdown` and `extractLearnings` there are multiple `regexp.MustCompile` calls inside the functions or loops, which allocates memory and parses the regex on every call.
2. **Optimize Regex Compilation**: Refactor these to be package-level global variables using `regexp.MustCompile`. This is a classic Go performance pattern to avoid runtime compilation overhead, as regexes are thread-safe once compiled.
3. **Regexes to elevate**:
   - `[^a-z0-9]+` in `normalizeTopicSegment` -> `var nonAlphanumericPattern = regexp.MustCompile(\`[^a-z0-9]+\`)`
   - `\n#{1,3} ` in `extractLearnings` -> `var nextHeaderPattern = regexp.MustCompile(\`\n#{1,3} \`)`
   - `(?m)^\s*\d+[.)]\s+(.+)` in `extractLearnings` -> `var numberedItemPattern = regexp.MustCompile(\`(?m)^\s*\d+[.)]\s+(.+)\`)`
   - `(?m)^\s*[-*]\s+(.+)` in `extractLearnings` -> `var bulletItemPattern = regexp.MustCompile(\`(?m)^\s*[-*]\s+(.+)\`)`
   - `\*\*([^*]+)\*\*` in `cleanMarkdown` -> `var boldPattern = regexp.MustCompile(\`\*\*([^*]+)\*\*\`)`
   - ``([^`]+)`` in `cleanMarkdown` -> `var inlineCodePattern = regexp.MustCompile("`([^`]+)`")`
   - `\*([^*]+)\*` in `cleanMarkdown` -> `var italicPattern = regexp.MustCompile(\`\*([^*]+)\*\`)`
4. **Implementation**: Modify `internal/mcp/tools_memory.go` to declare these variables at the package level and update the functions to use the pre-compiled regexes. Also, ensure comments explain the optimization.
5. **Verify**: Ensure the code builds, tests pass, and format check (`make fmt` / `go test ./...`) passes. Add an entry to `.jules/bolt.md` detailing the `regexp.MustCompile` learning.
6. **Pre-commit**: Complete pre-commit steps to ensure proper testing, verification, review, and reflection are done.
7. **Submit**: Create PR with a title "⚡ Bolt: [performance improvement]" and details.
