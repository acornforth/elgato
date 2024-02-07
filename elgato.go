package main

import "fmt"
import "time"
import "context"
import "encoding/json"
// import "io/ioutil"
import "net/http"
import "strings"

type Message struct {
    Id string `json:id` 
    Text string `json:text`
    Timestamp time.Time 
}

type BroadcastServer interface {
  Subscribe() <-chan Message
  CancelSubscription(<-chan Message)
}

type broadcastServer struct {
  source <-chan Message
  listeners []chan Message
  addListener chan chan Message
  removeListener chan (<-chan Message)
}

func (s *broadcastServer) Subscribe() <-chan Message {
  newListener := make(chan Message)
  s.addListener <- newListener
  return newListener
}


func (s *broadcastServer) CancelSubscription(channel <-chan Message) {
  s.removeListener <- channel
}

func NewBroadcastServer(ctx context.Context, source <-chan Message) BroadcastServer {
  service := &broadcastServer{
    source: source,
    listeners: make([]chan Message, 0),
    addListener: make(chan chan Message),
    removeListener: make(chan (<-chan Message)),
  }
  go service.serve(ctx)
  return service
}

func (s *broadcastServer) serve(ctx context.Context) {
  defer func () {
    for _, listener := range s.listeners {
      if listener != nil {
          close(listener)
      }
    }
  } ()

  for {
    select {
      case <-ctx.Done():
        return
      case newListener := <- s.addListener:
        s.listeners = append(s.listeners, newListener)
      case listenerToRemove := <- s.removeListener:
        for i, ch := range s.listeners {
          if ch == listenerToRemove {
              s.listeners[i] = s.listeners[len(s.listeners)-1]
              s.listeners = s.listeners[:len(s.listeners)-1]
              close(ch)
              break
          }
        }
      case val, ok := <-s.source:
        if !ok {
          return
        }
        for _, listener := range s.listeners {
          if listener != nil {
            select {
             case listener <- val:
             case <-ctx.Done():
              return
            }
            
          }
        }
    }
  }
}

var chat = make([]Message, 0)


func headers(w http.ResponseWriter, req *http.Request) {
    for name, headers := range req.Header {
        for _, h := range headers {
            fmt.Fprintf(w, "%v: %v\n", name, h)
            fmt.Printf("%v: %v\n", name, h)
        }
    }
}

func postMessageHandler(messageEvents chan<- Message) (func(http.ResponseWriter, *http.Request)) {
    fn := func(w http.ResponseWriter, req *http.Request) {
        switch req.Method {
        case http.MethodGet:
            fmt.Println("GET: /messages")
            for i,v := range chat[max(0,len(chat)-10):] {
                fmt.Fprintf(w, "<li id=\"%d\"><span>%s</span>%s</li>", i, v.Timestamp.Format("15:04:05.99"), v.Text)
            }
            // Serve the resource.
        case http.MethodPost:
            // Create a new record.
            fmt.Println("POST: /messages")
            dec := json.NewDecoder(req.Body);

            var message Message

            err:= dec.Decode(&message)
            if err != nil {
                w.WriteHeader(400)
                fmt.Fprintf(w, "Decode Error")
                return
            }

            message.Timestamp = time.Now()
            chat = append(chat,message)
            messageEvents <- message
            fmt.Fprint(w, `
            <input type="hidden" name="id" value="acorn1">
            <input type="text" name="text" placeholder="type a message...">

            `)
            fmt.Println(message.Id)
            fmt.Println(message.Text)
        case http.MethodPut:
            // Update an existing record.
        case http.MethodDelete:
            // Remove the record.
        default:
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        }

    }
    return fn
}

func html(w http.ResponseWriter, req *http.Request) {
    fmt.Fprint(w, `
<html>
<head>
    <link rel="stylesheet" href="/static/css/style.css">
    <script src="https://unpkg.com/htmx.org@1.9.10" integrity="sha384-D1Kt99CQMDuVetoL1lrYwg5t+9QdHe7NLX/SoJYkXDFfX37iInKRy5xLSi8nO7UC" crossorigin="anonymous"></script>
    <script src="https://unpkg.com/htmx.org/dist/ext/json-enc.js"></script>
    <script src="https://unpkg.com/htmx.org/dist/ext/sse.js"></script>
</head>
<body>
<script>
htmx.on("htmx:afterSwap", function(evt) {
    document.querySelector("input").focus();
}
</script>
<div id="chat-root">
<div hx-ext="sse" sse-connect="/events">
<ul id="messages" hx-get="/messages" hx-trigger="load,sse:new-messages">
</ul>
</div>
<!--div hx-ext="sse" sse-connect="/events" sse-swap="new-messages">
      Contents of this box will be updated in real time
      with every SSE message received from the chatroom.
  </div-->

<form action="POST" hx-post="/messages" hx-ext="json-enc" hx-swap="innerHTML">
<input type="hidden" name="id" value="acorn1">
<input type="text" name="text" placeholder="type a message...">
</div>
</body>
</html>`)
}

func formatSSE(message Message) (string, error) {
    sb := strings.Builder{}

	sb.WriteString(fmt.Sprintf("event: %s\n", "new-messages"))
	sb.WriteString(fmt.Sprintf("data: %v\n\n", message.Text))

	return sb.String(), nil
}
func eventsHandler(server BroadcastServer) (func(http.ResponseWriter, *http.Request)) {
    fn := func(w http.ResponseWriter, r *http.Request) {

        fmt.Println("establishing SSE connection")
        w.Header().Set("Content-Type", "text/event-stream")

        flusher, ok := w.(http.Flusher)
        if !ok {
            http.Error(w, "SSE not supported", http.StatusInternalServerError)
            return
        }

        listener := server.Subscribe()

        for {
            select {
            case <-r.Context().Done():
                return
            case message := <- listener:
                event, err := formatSSE(message)
                if err != nil {
                    fmt.Println(err)
                    break
                }

                _, err = fmt.Fprint(w, event)
                if err != nil {
                    fmt.Println(err)
                    break;
                }
                fmt.Println("Flushing...")
                flusher.Flush()
            }
        }
    }
    return fn
}

func main() {
    ctx, cancel := context.WithCancel(context.Background())

    defer cancel()
    
    messageEvents := make(chan Message);

    chatBroadcaster := NewBroadcastServer(ctx, messageEvents)

    fmt.Println("¿Qué pasa?")
    http.HandleFunc("/messages", postMessageHandler(messageEvents))
    http.HandleFunc("/headers", headers)
    http.HandleFunc("/events", eventsHandler(chatBroadcaster))
    http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
    http.HandleFunc("/html", html)
    //http.HandeFunc("/messages", postMessage)

    http.ListenAndServe(":4090", nil)
}
