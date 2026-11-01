# Text after ai is a prompt, not PowerShell. On Enter, this puts the stop-parsing
# token after ai when the prompt holds a character PowerShell would read, so an
# apostrophe does not open a string and $, (), {}, commas and ; are not
# evaluated. A plain prompt stays as typed. A | after the prompt still pipes.
Set-PSReadLineKeyHandler -Key Enter -BriefDescription AcceptLineWithAiPrompt -ScriptBlock {
	$ast = $null
	$tokens = $null
	$errors = $null
	$cursor = $null
	[Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$ast, [ref]$tokens, [ref]$errors, [ref]$cursor)
	# The tokens tell the ai command from ai inside a string or a comment. Inside
	# brackets, --% would also swallow the closing bracket, so those are left alone.
	$depth = 0
	foreach ($token in $tokens) {
		switch ($token.Kind) {
			{ $_ -in 'LParen', 'LCurly', 'AtParen', 'AtCurly', 'DollarParen' } { $depth++ }
			{ $_ -in 'RParen', 'RCurly' } { $depth-- }
		}
		if ($depth -ne 0 -or $token.Text -notmatch '^ai(\.exe)?$' -or
			-not ($token.TokenFlags -band [System.Management.Automation.Language.TokenFlags]::CommandName)) {
			continue
		}
		# Also left alone: --config lines, which keep quoted paths working, a line
		# that already has --%, and a prompt the user quoted.
		$rest = $ast.Extent.Text.Substring($token.Extent.EndOffset)
		$flags = [regex]::Match($rest, '^(\s+(-p|-m|--provider|--model)\s+[^\s|]+|\s+-[^\s|]+)*').Value
		$prompt = ($rest.Substring($flags.Length) -split '\|', 2)[0]
		# Outside Windows, PowerShell expands ? and * in the arguments of a program.
		$plain = if ($IsWindows -ne $false) { '^[\p{L}\p{N}\s.:/!?*+=_%^\\-]*$' } else { '^[\p{L}\p{N}\s.:/!+=_%^\\-]*$' }
		if ($flags -notmatch '\s--(config|%)(\s|=|$)' -and $prompt -match '^\s+[^\s''"]' -and $prompt -notmatch $plain) {
			[Microsoft.PowerShell.PSConsoleReadLine]::Replace($token.Extent.EndOffset, 0, ' --%')
		}
		break
	}
	[Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
}
