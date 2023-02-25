on run
	global activePID, activeName, windowName
	set activeName to ""
	set windowName to ""
	tell application "System Events"
		set activeApp to first application process whose frontmost is true
		set activePID to unix id of activeApp
		set activeName to name of activeApp
		tell activeApp
			try
				tell (1st window whose value of attribute "AXMain" is true)
					set windowName to value of attribute "AXTitle"
				end tell
			end try
		end tell
	end tell
	return ("{\"pid\":" & activePID & ",\"name\":" & quoteJSONString(activeName) & ",\"window\":" & quoteJSONString(windowName) & "}" as text)
end run

on quoteJSONString(val)
	set codepoints to id of val
	if (class of codepoints) is not list
		set codepoints to {codepoints}
	end

	set escaped to ""
	repeat with cp in codepoints
		set cp to cp as integer
		if cp = 34
			set q to "\\\""
		else if cp = 92 then
			set q to "\\\\"
		else if 32 <= cp and cp < 127
			set q to character id cp
		else
			set q to uEncode(cp)
		end
		set escaped to escaped & q
	end
	return "\"" & escaped & "\""
end

on uEncode(c)
	set digits to "0123456789abcdef"

	set hex to ""
	repeat until length of hex = 4
		set d to (c mod 16)
		set c to (c - d)/16 as integer
		set hex to (character (1+d) of digits) & hex
	end
	return "\\u" & hex
end

