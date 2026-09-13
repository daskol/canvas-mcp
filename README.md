# Canvas MCP server

*A local read-only MCP server for Instructure Canvas LMS.*

Connect an MCP client to your Canvas account to browse courses, read materials,
and check upcoming work. The server runs locally and communicates over stdio.

## Canvas API Exposed

The server exposes 13 tools covering selected read operations. Access follows
your Canvas account's permissions.

| API Group        | Supported Operations                                                        |
| ---------------- | --------------------------------------------------------------------------- |
| Courses          | List courses: read details, syllabi, terms, and available scores.           |
| Assignments      | List and read assignments, due dates, and available submission info.        |
| Modules          | List modules and their items                                                |
| Pages            | List and read course pages                                                  |
| Files            | List and read metadata and download URLs (no file downloads or extraction). |
| Announcements    | List published course announcements                                         |
| Planner          | Read upcoming work across courses                                           |
| Users            | Read your own profile                                                       |
| Other API groups | Not implemented.                                                            |

## Configuration

For clients that accept an `mcpServers` JSON configuration, use absolute paths
as follows.

```json
{
  "mcpServers": {
    "canvas": {
      "command": "/absolute/path/to/canvas-mcp",
      "args": [
        "--base-url", "https://canvas.example.edu",
        "--token-file", "/absolute/path/to/token"
      ]
    }
  }
}
```

Store your [Canvas access token][auth] in the token file.

| Flag           | Environment Fallback | Default                 |
| -------------- | -------------------- | ----------------------- |
| `--base-url`   | `CANVAS_BASE_URL`    | https://lms.skoltech.ru |
| `--token-file` | `CANVAS_TOKEN_FILE`  | `token`                 |

Flags take precedence over environment variables. Set `--base-url` or
`CANVAS_BASE_URL` to your institution's Canvas HTTPS origin without a path.

[auth]: https://developerdocs.instructure.com/services/canvas/oauth2/file.oauth
