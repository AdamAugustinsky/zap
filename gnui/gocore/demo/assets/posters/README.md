# Demo video posters

The first frames of upstream's stock clips in `../videos`, which the demo
core sends as its video messages' thumbnails, as real WhatsApp video messages
carry a JPEG preview. They share the clips' source and licence (NASA, public
domain; see `../SOURCES.md`). From `gnui/gocore/demo/assets`:

```sh
ffmpeg -i videos/launch.mp4 -frames:v 1 -vf scale=320:180 -q:v 5 posters/launch.jpg
ffmpeg -i videos/note.mp4 -frames:v 1 -vf scale=240:240 -q:v 5 posters/note.jpg
```
