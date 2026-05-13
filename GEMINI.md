# Development Guidelines

## Go Compilation
- **Do NOT attempt to compile Go locally.** The local environment does not have Go installed.
- Rely on **GitHub Actions** for compilation. Pushing changes to the repository will trigger a build.

## Shell Commands (Windows/PowerShell)
- Use `curl.exe` instead of `curl` to ensure you are using the real curl binary rather than the PowerShell alias `Invoke-WebRequest`.
- Use `Select-String` instead of `grep` for searching through command output.
  - Example: `curl.exe -s "..." | Select-String -Pattern "pattern"`
- Use `;` instead of `&&` for chaining commands in PowerShell.

## API & Metadata
- The upstream API (`qdl-api.monochrome.tf`) sometimes returns items wrapped in a `{"type": "...", "content": "..."}` structure, and sometimes as direct objects.
- Ensure duration is parsed correctly from the correct level depending on the item type.
