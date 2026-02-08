# Charmbracelet VHS tape — regenerate docs/assets/demo.gif
# Install: https://github.com/charmbracelet/vhs
# Run from repo root with a working demo cluster + CLI on PATH:
#   vhs docs/demo/kubelease.tape

Output docs/assets/demo.gif
Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 700
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Type "kubectl kubelease create preview --ttl 30m --max-ttl 2h --policy preview-default --owner demo"
Enter
Sleep 3s

Type "kubectl kubelease list"
Enter
Sleep 2s

Type "kubectl kubelease expire preview"
Enter
Sleep 3s

Type "kubectl kubelease list"
Enter
Sleep 2s
