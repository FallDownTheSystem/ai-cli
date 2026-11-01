# Text after ai is a prompt, not shell syntax. On Enter, this quotes the prompt
# up to the first |, so an apostrophe does not open a string and ?, *, $ and ()
# are not expanded. A | after the prompt still pipes.
_ai_quote_prompt() {
	emulate -L zsh
	# emulate -L keeps these; with bash_rematch, =~ would leave $match empty.
	setopt no_bash_rematch no_re_match_pcre
	local -a match mbegin mend
	local flags='(([[:space:]]+(-p|-m|--provider|--model)[[:space:]]+[^[:space:]|]+)|([[:space:]]+-[^[:space:]|]+))*'
	# A buffer of several lines is left alone: where its prompt ends cannot be told.
	[[ $BUFFER != *$'\n'* && $BUFFER =~ "^(([^|]*\\|)*[[:space:]]*ai${flags})[[:space:]]+([^-|[:space:]][^|]*)(.*)\$" ]] || return
	local head=$match[1] text=$match[7] tail=$match[8] quotes
	text=${text%"${text##*[![:space:]]}"}
	quotes=${text//[^\"]/}
	# Left alone: ai that may sit inside quotes, a subshell or a block, or follow
	# ; or &&; --config lines; a prompt the user quoted or escaped, which includes
	# a line recalled from history; and an odd number of ", where a quoted | cut
	# the prompt short.
	[[ $head == *--config* || $head == *[\'\"\`\\\(\)\{\}\;\&\#\<\>]* ]] && return
	[[ $text == [\'\"]* || $text == *\\* ]] && return
	(( ${#quotes} % 2 )) && return
	BUFFER="$head ${(qq)text}${tail:+ $tail}"
}
_ai_accept_line() {
	_ai_quote_prompt
	zle _ai_orig_accept_line -- "$@"
}
# Enter still runs whatever accept-line did before, such as another plugin's wrapper.
if [[ ${widgets[accept-line]} != user:_ai_accept_line ]]; then
	case ${widgets[accept-line]} in
		user:*) zle -N _ai_orig_accept_line ${widgets[accept-line]#user:} ;;
		*) zle -A .accept-line _ai_orig_accept_line ;;
	esac
	zle -N accept-line _ai_accept_line
fi
