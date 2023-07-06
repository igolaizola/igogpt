# igoGPT

**igoGPT** is a tool inspired by AutoGPT and implemented in Golang.

> There are too many Pythons in the AI world. Gophers can do AI too!

This is a work in progress, so expect bugs and missing features.

## 🚀 Features

### Bing chat-based searches

Auto mode can use Bing Chat to access the internet.
This is much better than using a search engine like Google because the results are richer than just a list of links.

### ChatGPT option for GPT4

GPT4 queries using OpenAI API can be expensive.
This option uses ChatGPT and your browser to perform the queries using a chat window.

### Multiple command support

Auto mode responses can trigger more than one command each time.
This allows more interaction in fewer steps.

### Pair mode

Connect one chat with another chat and let them talk to each other.

### Bulk mode

Generate a list of prompts and run all of them one by one.

### Chats that implement `io.ReadWriter`

You can import the libraries in the `pkg` directory to use Bing or ChatGPT as `io.ReadWriter` in your own projects.

## 📝 TODO list

 - Web command: open a web page in the browser instead of using a http client.
 - Memory: use chroma, pinecone or similar to manage OpenAI chat memory.
 - ChatGPT: transfer to a new chat when the current one has ended.
 - ChatGPT: process errors when GPT4 is not available.
 - OpenAI: retry requests when the API is not available.
 - Allow user input in auto mode.
 - Add more commands.
 - Drink more coffee.

## 📦 Installation

You can use the Golang binary to install **igoGPT**:

```bash
go install github.com/igolaizola/igogpt/cmd/igogpt@latest
```

Or you can download the binary from the [releases](https://github.com/igolaizola/igogpt/releases)

## 🕹️ Usage

### Configuration

To launch **igoGPT** you need to configure different settings.
Go the parameters section to see all available options: [Parameters](#%EF%B8%8F-parameters)

Using a configuration file in YAML format:

```bash
igogpt auto --config igogpt.yaml
```

```yaml
# igogpt.yaml
model: gpt-4
openai-key: OPENAI_KEY
google-key: GOOGLE_KEY
google-cx: GOOGLE_CX
chatgpt-remote: http://localhost:9222
bing-session: bing-session.yaml
goal: |
    Implement a hello world program in Go in different languages.
    The program takes the language as a parameter.
```

Using environment variables (`IGOGPT` prefix, uppercase and underscores):

```bash
export IGOGPT_MODEL=gpt-4
export IGOGPT_OPENAI_KEY=OPENAI_KEY
export IGOGPT_GOOGLE_KEY=GOOGLE_KEY
export IGOGPT_GOOGLE_CX=GOOGLE_CX
export IGOGPT_CHATGPT_REMOTE=http://localhost:9222
export IGOGPT_BING_SESSION=bing-session.yaml
export IGOGPT_GOAL="Implement a hello world program in Go in different languages. The program takes the language as a parameter."
igogpt auto
```

Using command line arguments:

```bash
igogpt auto --model gpt-4 --openai-key OPENAI_KEY --google-key GOOGLE_KEY --google-cx GOOGLE_CX --chatgpt-remote http://localhost:9222 --bing-session bing-session.yaml --goal "Implement a hello world program in Go in different languages. The program takes the language as a parameter."
```
### Commands

#### Auto mode

Auto mode will initiate a conversation with the chatbot and will try to achieve the goal.
Every step the chatbot will send commands and the program will execute them.

```bash
igogpt auto --config igogpt.yaml
```

#### Pair mode

Pair mode will connect two chats and let them talk to each other.
The first chat will receive the initial prompt with the orders and then will start the conversation with the second chat.
They will try to achieve the goal together.

```bash
igogpt pair --config igogpt.yaml
```

#### Chat mode

Launch a interactive chat with the chatbot.
Use standard input and output to communicate with the chatbot.

```bash
igogpt chat --config igogpt.yaml
```

#### Bulk mode

Bulk mode reads a list of prompts from a file and runs them one by one. You can group prompts so that each group runs in a different chat.

```bash
igogpt bulk --config igogpt.yaml --bulk-in prompts.txt --bulk-out output.json
```

The input file can be a JSON file with a list (or lists) of prompts.
Each sublist will be launched in a different chat.

```json
[
    ["prompt 1 in chat 1", "prompt 2 in chat 1"],
    ["prompt 1 in chat 2", "prompt 2 in chat 2"],
]
```

Alternatively, the input can be a text file with one prompt per line.
Use an empty line to separate prompts in different chats.

```text
prompt 1 in chat 1
prompt 2 in chat 1

prompt 1 in chat 2
prompt 2 in chat 2
```

The output file will be a JSON file containing all the prompts and their corresponding responses.

```json
[
    [
        {
            "in": "prompt 1 in chat 1",
            "out": "response 1 in chat 1"
        },
        {
            "in": "prompt 2 in chat 1",
            "out": "response 2 in chat 1"
        },
    ],
    [
        {
            "in": "prompt 1 in chat 1",
            "out": "response 1 in chat 1"
        },
        {
            "in": "prompt 2 in chat 1",
            "out": "response 2 in chat 1"
        },
    ],
]
```

### Create bing session (only for the first time)

If you want to use the Bing search engine, you need to create a session file with the Bing cookies and other information retrieved from your browser.

Use the `igogpt create-bing-session` command to create the session file.

You can use the `--remote` flag to specify the remote debug URL of the browser to use.

```bash
igogpt create-bing-session --remote "http://localhost:9222"
```

Or you can try letting the program to launch the browser automatically:

```bash
igogpt create-bing-session
```

## 🛠️ Parameters

You can use the `--help` flag to see all available options.

### Global parameters

 - `config` (string) path to the configuration file.
 - `ai` (string) ai chat to use. Available options: `chatgpt`, `openai`.
 - `goal` (string) goal to achieve.
 - `prompt` (string) use your own prompt instead of the default one. The goal will be ignored.
 - `model` (string) model to use. Available options: `gpt-4`, `gpt-3.5-turbo`.
 - `proxy` (string) proxy to use.
 - `output` (string) output directory for commands.
 - `log` (string) directory to save the log of the conversation, if empty the log will be only printed to the console.
 - `steps` (int) number of steps to run, if 0 it will run until the goal is achieved or indefinitely.

### Bulk parameteres

 - `bulk-in` (string) path to the input file with the prompts.
 - `bulk-out` (string) path to the output file with the responses.

### Google parameters

 - `google-key` (string) google api key.
 - `google-cx` (string) google custom search engine id.	

### OpenAI parameters

 - `openai-wait` (duration) wait time between requests (e.g. 5s).
 - `openai-key` (string) openai api key.
 - `openai-max-tokens` (int) max tokens to use in each request.

### ChatGPT parameters

 - `chatgpt-wait` (duration) wait time between requests (e.g. 5s).
 - `chatgpt-remote` (string) remote debug url of the browser to use with chatgpt.

### Bing parameters

 - `bing-wait` (duration) wait time between requests (e.g. 5s).
 - `bing-session` (string) path to the bing session file.
 
## ❓ FAQ

### How do I launch a browser with remote debugging enabled?

You must launch the binary of your browser and with the `--remote-debugging-port` flag.

#### Chrome example in windows

```bash
"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe" --profile-directory="Default" --remote-debugging-port=9222 --remote-debugging-address=0.0.0.0
```

#### Microsoft Edge example in windows

In edge is recommended to use a different user data directory to avoid conflicts with your main browser.

```bash
"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222 --user-data-dir="C:\Users\myuser\EdgeDebug"
```

### How can I launch the browser in one computer and use it in another?

You can use [ngrok](https://ngrok.com/) to expose the remote debugging port of the browser to the internet.

```bash
ngrok tcp 9222
```

The url will be something like this: `tcp://0.tcp.ngrok.io:12345`.
You need to change it to `http://ip:port` format.
Use `ping 0.tcp.ngrok.io` to get the ip address.

This also works if you are having troubles to connect from WSL to Windows.

## ⚠️ Disclaimer

The automation of Bing Chat and ChatGPT accounts is a violation of their Terms of Service and will result in your account(s) being terminated.

Read about Bing Chat and ChatGPT Terms of Service and Community Guidelines.

**igoGPT** was written as a proof of concept and the code has been released for educational purposes only.
The authors are released of any liabilities which your usage may entail.

## 💖 Support

If you have found my code helpful, please give the repository a star ⭐

Additionally, if you would like to support my late-night coding efforts and the coffee that keeps me going, I would greatly appreciate a donation.

You can invite me for a coffee at ko-fi (0% fees):

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/igolaizola)

Or at buymeacoffee:

[![buymeacoffee](https://user-images.githubusercontent.com/11333576/223217083-123c2c53-6ab8-4ea8-a2c8-c6cb5d08e8d2.png)](https://buymeacoffee.com/igolaizola)

Donate to my PayPal:

[paypal.me/igolaizola](https://www.paypal.me/igolaizola)

Sponsor me on GitHub:

[github.com/sponsors/igolaizola](https://github.com/sponsors/igolaizola)

Or donate to any of my crypto addresses:

 - BTC `bc1qvuyrqwhml65adlu0j6l59mpfeez8ahdmm6t3ge`
 - ETH `0x960a7a9cdba245c106F729170693C0BaE8b2fdeD`
 - USDT (TRC20) `TD35PTZhsvWmR5gB12cVLtJwZtTv1nroDU`
 - USDC (BEP20) / BUSD (BEP20) `0x960a7a9cdba245c106F729170693C0BaE8b2fdeD`
 - Monero `41yc4R9d9iZMePe47VbfameDWASYrVcjoZJhJHFaK7DM3F2F41HmcygCrnLptS4hkiJARCwQcWbkW9k1z1xQtGSCAu3A7V4`

Thanks for your support!

## 📚 Resources

Some of the resources I used to create this project:

 - [Significant-Gravitas/Auto-GPT](https://github.com/Significant-Gravitas/Auto-GPT) is the main inspiration for this project.
 - [tiktoken-go/tokenizer](https://github.com/tiktoken-go/tokenizer) to count tokens before sending the prompt to OpenAI.
 - [pavel-one/EdgeGPT-Go](https://github.com/pavel-one/EdgeGPT-Go) to connect to Bing Chat.
 - [PullRequestInc/go-gpt3](https://github.com/PullRequestInc/go-gpt3) to send requests to OpenAI.
 - [Danny-Dasilva/CycleTLS](https://github.com/Danny-Dasilva/CycleTLS) to mimic the browser when connecting to Bing Chat.
 - [chromedp/chromedp](https://github.com/chromedp/chromedp) to control the browser from golang code.
