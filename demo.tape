# charmbracelet/vhs tape for demo.gif: run `vhs demo.tape` locally or trigger
# the demo workflow (Actions > demo > run workflow) after changing the renderer
Output demo.gif
Set FontSize 14
Set Width 1280
Set Height 640
Set Padding 12

Hide
Type "go build -o /tmp/agentdash-demo-bin ./cmd/agentdash && source tests/demo-env.sh && source tools/fake-proc.sh /tmp/agentdash-demo/proc && ln -sf /tmp/agentdash-demo-bin /tmp/agentdash-demo/bin/agentdash && clear"
Enter
Sleep 3s
Show

Type "agentdash"
Enter
Sleep 3.5s

Type "agentdash -w"
Enter
Sleep 2.5s
Type "j"
Sleep 800ms
Type "j"
Sleep 1.2s
Type "s"
Sleep 3.5s
Type " "
Sleep 1.5s
Type "t"
Sleep 3s
Type "q"
Sleep 1.5s
