# Web UI demo — browser-based dashboard
# NOTE: This one is best recorded with OBS or screen capture
# since it shows a browser. Use this tape for the terminal side:
#   1. Run `vhs web.tape` to capture the server startup
#   2. Screen-record the browser at localhost:3000 separately
#   3. Combine with ffmpeg if needed
Output web.webm
Output web.gif

Set Width 900
Set Height 500
Set FontSize 15
Set FontFamily "JetBrains Mono"
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 60ms

Type "# Start the web dashboard"
Enter
Sleep 1s

Type "daedalus web"
Enter
Sleep 3s

Type "# Open http://localhost:3000 in your browser"
Enter
Sleep 2s

Type "# The web UI provides:"
Enter
Type "#   - Project list with start/stop controls"
Enter
Type "#   - Embedded terminal via xterm.js"
Enter
Type "#   - Real-time status updates"
Enter
Sleep 3s
