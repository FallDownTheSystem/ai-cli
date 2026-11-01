# Text after ai is a prompt, not shell syntax. On Enter, this quotes the prompt
# up to the first |, so an apostrophe does not open a string and $, * and ()
# are not expanded. A | after the prompt still pipes.
function _ai_accept_line
	set -l re '^(?<_ai_head>(?:[^|]*\|)*\s*ai(?:\s+(?:-p|-m|--provider|--model)\s+[^\s|]+|\s+-[^\s|]+)*)\s+(?<_ai_prompt>[^-|\s][^|]*?)\s*(?<_ai_tail>\|.*)?$'
	set -l buffer (commandline)
	# Left alone: a buffer of several lines, where the end of the prompt cannot be
	# told; ai that may sit inside quotes, a subshell or a block, or follow ; or &&;
	# --config lines; a prompt the user quoted or escaped, which includes a line
	# recalled from history; and an odd number of ", where a quoted | cut the
	# prompt short.
	if test (count $buffer) -eq 1
		and string match -qr -- $re $buffer
		and not string match -qr -- '--config|[\'"`\\\\(){};&#<>]' $_ai_head
		and not string match -qr -- '^[\'"]|\\\\' $_ai_prompt
		and string match -qr -- '^[^"]*("[^"]*"[^"]*)*$' $_ai_prompt
		set -l line "$_ai_head "(string escape -- $_ai_prompt)
		if test -n "$_ai_tail"
			set line "$line $_ai_tail"
		end
		commandline -r -- $line
	end
	commandline -f execute
end
bind \r _ai_accept_line
