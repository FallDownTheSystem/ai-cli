# Text after ai is a prompt, not shell syntax. On Enter, this quotes the prompt
# up to the first |, so an apostrophe does not open a string and ?, *, $ and ()
# are not expanded. A | after the prompt still pipes. Needs bash 4.4 or newer.
_ai_quote_prompt() {
	local flags='(([[:space:]]+(-p|-m|--provider|--model)[[:space:]]+[^[:space:]|]+)|([[:space:]]+-[^[:space:]|]+))*'
	local re="^(([^|]*\\|)*[[:space:]]*ai${flags})[[:space:]]+([^-|[:space:]][^|]*)(.*)\$"
	# A buffer of several lines is left alone: where its prompt ends cannot be told.
	[[ $READLINE_LINE != *$'\n'* && $READLINE_LINE =~ $re ]] || return
	local head=${BASH_REMATCH[1]} prompt=${BASH_REMATCH[7]} tail=${BASH_REMATCH[8]} quotes
	prompt=${prompt%"${prompt##*[![:space:]]}"}
	quotes=${prompt//[^\"]}
	# Left alone: ai that may sit inside quotes, a subshell or a block, or follow
	# ; or &&; --config lines; a prompt the user quoted or escaped, which includes
	# a line recalled from history; and an odd number of ", where a quoted | cut
	# the prompt short.
	[[ $head == *--config* || $head == *[\'\"\`\\\(\)\{\}\;\&\#\<\>]* ]] && return
	[[ $prompt == [\'\"]* || $prompt == *\\* ]] && return
	(( ${#quotes} % 2 )) && return
	READLINE_LINE="$head ${prompt@Q}${tail:+ $tail}"
	READLINE_POINT=${#READLINE_LINE}
}
# Enter runs the rewrite through a spare key sequence, then accepts the line.
bind -x '"\C-x\C-a": _ai_quote_prompt'
bind '"\C-m": "\C-x\C-a\C-j"'
