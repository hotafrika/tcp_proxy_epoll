## TCP proxy (version 2)

### Main idea
After implementing the first version of TCP I was super curious to implement the version which could support much more connections.
The main idea of this version is to use linux Epoll for incoming and remote connections. It allows us to serve connections only when
they have data for reading or connections were closed.

This is definitely NOT an example of good Epoll implementation (on the contrary, it is implemented in the most simplified form).
Rather, this is a "TCP proxy using Epoll" proof-of-concept.

### Summary
This is a multi-backend and multi-frontend TCP proxy. The proxy is configured using JSON config file and available flags.
Config file contains configuration info about available "apps", every "app" has 1-N ports ("frontends") and 1-M "backends".

Let's take a look at all details:

Graceful shutdown is implemented. NotifyContext is used to control the execution of all goroutines and some operations.
On start, TCP proxy starts service goroutines for every frontend and backend.

Every frontend and backend has its created Epoll instance to serve connections related to this frontend or backend.

On start, every frontend tries to listen on the specified port. If the port is busy, frontend will continue to try to create a listener on the port until success or ctx is done.
When the listener is successfully created, frontend starts to accept new incoming connections. Also, this frontend's Epoll starts to listen for new events.

On start, every backend starts the goroutine with healthcheck (active healthcheck) to know if the backend endpoint is available.
Also, when a new connection to the backend endpoint failed, so this backend gets "unavailable" state (passive healthcheck) until next successful active healthcheck.
Also, this backend's Epoll starts to listen for new events.

Epoll wrapper is implemented only for linux, and it has the most simplified form. It listens only for EPOLLIN, EPOLLHUP, EPOLLRDHUP events.

On a new incoming connection to a frontend, its app checks if there are available backends. The app chooses the backend with the least active connections.
Then, a new remote connection is created to the chosen backend endpoint.
The incoming connection and remote connection are wrapped in PipedConn and added to epoll instances.

When any epoll has events on it, related connections are processed according to events type.
Every event starts new goroutines to execute IO operations.
Because several events can be created for one existing connections, we use bool flag field ("underIO" field of PipedConn) to avoid multiple IO operations (in the same direction) for one connection.

IO operations stop on an error or 0 bytes copying. The IO goroutine exits and both connections are closed and deleted from related epoll instances.

To copy data between two connections io.CopyBuffer is used in combination with sync.Pool for buffers. It allows for decreasing memory allocations.

Of course, the implementation can be improved in many directions.
For example, the main trade-offs for this version were the implementation of Epoll and epoll events handling (for example, to close connections on EPOLLHUP, EPOLLRDHUP events, and execute reading on EPOLLIN).
I intentionally left this part simple (and possibly a bit not correct) because it wasn't the goal of this challenge.

Also, one of the most critical part is epoll event processing. Now we create a new goroutine to process one event. 
The ideal solution here is to use goroutine pool for this task.

Let me know if you think these trade-offs are important for this challenge, I will fix it =)
Other possible improvements are discussed in the section ["Your questions"](#your-questions).

### Available flags:
* -config FILENAME - path to the JSON config file, default "config.json";
* -loglevel LEVEL - log level, default 0. Possible values range is 0-7, where 0=debug, 1=info, 2=warn, 3=error, 4=fatal, 5=panic, .. 7=disabled;
* -pprof - starts pprof web server on port 6060.

### Launch examples:

Starts proxy with loglevel=debug, config filepath "config.json", without pprof enabled.
```bash
cmd> go run main.go
```

Starts proxy with loglevel=error, config filepath "some.json", with pprof enabled on port 6060.
```bash
cmd> go run -config some.json -loglevel 3 -pprof omain.go
```
