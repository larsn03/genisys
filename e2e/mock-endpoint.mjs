// Stateless OpenAI-compatible mock for e2e (IMPL §"Mock endpoint"):
// behavior derives from the request messages, so retries/replays are safe.
//  - last user text matches /sleep/ and no tool result yet → shell sleep tool call
//  - otherwise → "All finished."
import http from "node:http";

const port = Number(process.env.MOCK_PORT ?? 8799);

http
  .createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      let j = {};
      try {
        j = JSON.parse(body || "{}");
      } catch {
        /* keepalive */
      }
      const msgs = j.messages ?? [];
      const hasTool = msgs.some((m) => m.role === "tool");
      const lastUser = [...msgs].reverse().find((m) => m.role === "user")?.content ?? "";
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      const sse = (o) => res.write(`data: ${JSON.stringify(o)}\n\n`);
      if (/sleep/i.test(String(lastUser)) && !hasTool) {
        sse({
          choices: [
            {
              delta: {
                tool_calls: [
                  {
                    index: 0,
                    id: "e2e_call_1",
                    type: "function",
                    function: {
                      name: "shell_command",
                      arguments: JSON.stringify({ command: "sleep 3", timeout_ms: 8000 }),
                    },
                  },
                ],
              },
            },
          ],
        });
      } else {
        sse({ choices: [{ delta: { content: "All finished." } }] });
      }
      res.write("data: [DONE]\n\n");
      res.end();
    });
  })
  .listen(port, "127.0.0.1", () => console.log(`mock endpoint on 127.0.0.1:${port}`));
