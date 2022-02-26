# Plug-N-Meet - Scalable, Open source web conference system

Plug-N-Meet is an open source web conferencing system based on high performance WebRTC
infrastructure [livekit](https://github.com/livekit/livekit-server).

## Features:

1) Compatible with all devices. Browser recommendation: Google Chrome, Firefox. For iOS: Safari.
2) WebRTC based secured & encrypted communication.
3) Easy integration with any existing website or system.
4) Easy customization with functionality, URL, logo, and branding colors.
5) HD audio and video call.
6) HD Screensharing.
7) Lock settings.
8) Raise hand.
9) Chatting with File sharing.
10) MP4 Recordings.
11) RTMP Broadcasting

And many more!

The components of Plug-N-Meet are as follows:

1) [plugNmeet-server](https://github.com/mynaparrot/plugNmeet-server), the main backend server written in go.

2) [plugNmeet-client](https://github.com/mynaparrot/plugNmeet-client), which is the main interface/frontend. It's built
   with React and Redux.

3) [plugNmeet-recoder](https://github.com/mynaparrot/plugNmeet-recorder), a node module for recording/rtmp broadcasting
   which is written in TypeScript.

## Requirements

1) Livekit configured with Redis.
2) `plugNmeet-server` configured with same Redis instance using for livekit.
3) Mariadb server for data storage.

We've created an easy to install script which can be used to install all the necessary components in 5 minutes.
Check [plugNmeet-install](https://github.com/mynaparrot/plugNmeet-install) repo.

## SDKs & Tools

**SDK**

1) [PHP](https://github.com/mynaparrot/plugNmeet-sdk-php)

Following ready to use extensions:

1) [Joomla component](https://github.com/mynaparrot/plugNmeet-joomla)
2) [Moodle Plugin](https://github.com/mynaparrot/plugNmeet-moodle)
3) [Wordpress Plugin](https://github.com/mynaparrot/plugNmeet-wordpress)

Examples:

1) [Example of API](https://github.com/mynaparrot/plugNmeet-server/wiki/API-Information-(examples))

## Manually

Create `config.yaml`
from [config_sample.yaml](https://raw.githubusercontent.com/mynaparrot/plugNmeet-server/main/config_sample.yaml) &
change necessary info

***Using docker***

```
docker run --rm -p 8080:8080 \
    -v $PWD/config.yaml:/config.yaml \
    mynaparrot/plugnmeet-server \
    --config /config.yaml \
```

You can also
follow [docker-compose_sample.yaml](https://raw.githubusercontent.com/mynaparrot/plugNmeet-server/main/docker-compose_sample.yaml)
file.

You can manually download server from [release](https://github.com/mynaparrot/plugNmeet-server/releases) page too.

## Development

1) Clone the project & navigate to the directory. Make sure you've docker install in your
   PC. https://www.docker.com/products/docker-desktop

2) Copy to rename this files:

```
cp config_sample.yaml config.yaml
cp livekit_sample.yaml livekit.yaml
cp docker-compose_sample.yaml docker-compose.yaml
```

3) Now run `docker-compose up --build` & wait to finish process. If you're running this first time then you may see some
   errors. Hit `control + c` or `ctrl + c` & re-run `docker-compose up --build`. Everytime need to run this command to
   boot servers.
