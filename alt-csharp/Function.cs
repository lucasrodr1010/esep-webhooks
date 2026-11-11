// ALTERNATE, NOT USED BY THE GO SOLUTION, NOT COMPILED OR RUN (no .NET SDK on
// the machine this was written on). It is the spec's own C# FunctionHandler
// (page 10) with the same defenses as the Go code: clear error if the
// SLACK_URL variable is missing, ignore events with no issue (GitHub's "ping",
// Postman's "test"), only forward github.com links, timeout under GitHub's
// 10 seconds, and never print the URL.
//
// To use it instead of Go: follow spec Part 1 (dotnet new lambda.EmptyFunction
// --name EsepWebhook), replace the template's Function.cs with this file,
// `dotnet add package Newtonsoft.Json`, and deploy with
// `dotnet lambda deploy-function EsepWebhook`.
using System;
using System.Net.Http;
using System.Text;
using Amazon.Lambda.Core;
using Newtonsoft.Json;
using Newtonsoft.Json.Linq;

[assembly: LambdaSerializer(typeof(Amazon.Lambda.Serialization.SystemTextJson.DefaultLambdaJsonSerializer))]

namespace EsepWebhook;

public class Function
{
    private static readonly HttpClient Client = new() { Timeout = TimeSpan.FromSeconds(8) };

    public string FunctionHandler(object input, ILambdaContext context)
    {
        context.Logger.LogLine($"Received: {input}");

        string? slackUrl = Environment.GetEnvironmentVariable("SLACK_URL");
        if (string.IsNullOrWhiteSpace(slackUrl))
            throw new InvalidOperationException(
                "SLACK_URL is not set: add it under Configuration -> Environment variables.");

        JToken? root;
        try { root = JToken.Parse(input.ToString() ?? ""); }
        catch (JsonException) { throw new ArgumentException("event is not valid JSON"); }

        // Not an issue event (ping, "test"): nothing to post.
        string? issueUrl = (root as JObject)?["issue"]?["html_url"]?.ToString();
        if (string.IsNullOrEmpty(issueUrl)) return "ignored: not an issue event";

        // Only forward https://github.com links (the API URL is public).
        if (!Uri.TryCreate(issueUrl, UriKind.Absolute, out var u) || u.Scheme != "https"
            || (u.Host != "github.com" && u.Host != "www.github.com"))
            throw new ArgumentException("refusing issue.html_url: only https://github.com links are forwarded");

        string payload = JsonConvert.SerializeObject(new { text = $"Issue Created: {u}" });
        var request = new HttpRequestMessage(HttpMethod.Post, slackUrl)
        {
            Content = new StringContent(payload, Encoding.UTF8, "application/json")
        };

        using var response = Client.Send(request);
        string body = new System.IO.StreamReader(response.Content.ReadAsStream()).ReadToEnd();
        if (!response.IsSuccessStatusCode)
            throw new InvalidOperationException(
                $"Slack answered HTTP {(int)response.StatusCode}: {body} (invalid_payload / no_service / channel_not_found mean SLACK_URL is wrong or revoked)");
        return body; // "ok"
    }
}
