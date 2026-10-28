# Demo video posters

The first frames of upstream's stock clips in `assets/demo/videos`, which the
demo core sends as its video messages' thumbnails, as real WhatsApp video
messages carry a JPEG preview. They share the clips' source and licence
(NASA, public domain; see `assets/demo/SOURCES.md`).

```sh
ffmpeg -i assets/demo/videos/launch.mp4 -frames:v 1 -vf scale=320:180 -q:v 5 zfcore/assets/posters/launch.jpg
ffmpeg -i assets/demo/videos/note.mp4 -frames:v 1 -vf scale=240:240 -q:v 5 zfcore/assets/posters/note.jpg
```
