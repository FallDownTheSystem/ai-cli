# Prepares the shell of a demo recording. The tapes dot-source it, so the prompt
# and the key handler stay defined in their session.
param([Parameter(Mandatory)][string]$Demo)

function prompt { '❯ ' }

# Without this, ai -c would read the terminal VHS was started from instead of the recording.
Remove-Item Env:KERO_PANE, Env:TMUX, Env:ZELLIJ, Env:STY, Env:WEZTERM_PANE, Env:KITTY_WINDOW_ID -ErrorAction SilentlyContinue

# ai's defaults, in place of the recording user's own settings.
$env:XDG_CONFIG_HOME = Join-Path $env:TEMP 'ai-demo-config'
Remove-Item -Recurse -Force $env:XDG_CONFIG_HOME -ErrorAction SilentlyContinue

ai --init pwsh | Out-String | Invoke-Expression

$dir = Join-Path $env:TEMP "ai-demo-$Demo"
Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
Set-Location (New-Item -ItemType Directory $dir)

switch ($Demo) {
	'execute' {
		# The last commit adds a file, so undoing it leaves the file staged.
		git init -q
		Set-Content README.md 'demo'
		git add .
		git commit -qm 'Initial commit'
		Set-Content notes.txt 'draft'
		git add .
		git commit -qm 'Add notes'
	}
	'continue' {
		go mod init demo 2>$null
		Set-Content main.go @'
package main

import "fmt"

func main() {
	fmt.Println("listening on", port)
}
'@
	}
}

Clear-Host
