# pgeu-meetingserver

This is the server component of the meeting system in
[pgeu-system](https://github.com/pgeu/pgeu-system/). It serves as a
basic relay for handling meetings, including polls and archiving of
the data. It is fully integrated with the `pgeu-system`, and needs
direct access to the PostgreSQL database of that system in order to
operate.

## Operating

The server should normally be served behind a proxy such as *nginx*,
which will be responsible for handling TLS termination and possible
access controls. It is intended to run continuously as a service, and
will open and close meetings as necessary based on the contents of the
database.

### Commandline syntax

`pgeu-meetingserver -origin origin [-behindproxy] [-dburl url] [-listen listen] [-profilelisten profilelisten]`

The following parameters can be set:

**-origin origin**
> This specifies a value to validate the origin header of the incoming
> http requests against. This would normally be the base URL of the
> website hosting the pgeu system (e.g. *https://www.domain.com/*). To
> disable origin validation (not recommended in production), specify the
> value `*`.

**-behindproxy**
> Including this flag tells the server to decode and use the value
> from the HTTP header `X-Forwarded-For` if one is present. This header
> is typically set by the proxy server, and if it is *not* set by the
> proxy server this flag should not be specified.

**-dburl url**
> Specifies a [Go lib/pq](https://pkg.go.dev/github.com/lib/pq) style
> database URL to connect to PostgreSQL. If it's not specified, the
> default value of `postgres:///postgresqleu` is used, which will
> connect to the database `postgresqleu` using a Unix domain socket.

**-listen listen**
> Specifies either a *host:port* combination or a Unix socket location
> to listen for incoming http requests on. If none is specified, the
> server will by default listen to *127.0.0.1:8199*.
> If the value starts with a slash, it is interpreted as the path of a
> Unix socket to listen on, and should not include a port number.

**-profilelisten profilelisten**
> Specifies listener in the same syntax as `-listen` that will allow
> access to the Go [profiler data](https://golang.org/pkg/net/http/pprof/)
> over http. This endpoint has no authentication and if used should
> only specify a local or otherwise already authenticated endpoint. If
> not specified, the profiler data is not made available.


### Nginx sample

When a service is run per above, serving the data over a Unix socket
for example, the following nginx config snippet shows up to set up
proxying to the server:

```
location /ws/meeting/ {
        include proxy_params;
        proxy_pass http://unix:/tmp/.meetingserver_socket;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_http_version 1.1;
        proxy_read_timeout 120;
}

```

As the websockets used are long lived, the `proxy_read_timeout` must
be set high enough. The server will send websocket ping/pong messages
every 60 seconds, so a value double that is a reasonable safety
margin.
