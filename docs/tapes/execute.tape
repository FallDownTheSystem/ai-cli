Output docs/execute.gif
Set Width 1100
Set Height 720
Source docs/tapes/setup.tape

Hide
Type ". ./docs/tapes/prepare.ps1 execute"
Enter
Wait
Show

Type "git log --oneline"
Enter
Wait
Sleep 1s
Type "ai -e undo my last commit but keep the changes"
Sleep 400ms
Enter
Wait+Screen /revise/
Sleep 2s
Type "y"
Wait
Sleep 500ms
Type "git status --short"
Enter
Wait
Sleep 6s
