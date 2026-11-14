window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: './openapi.yaml',
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis],
    layout: 'BaseLayout',
    defaultModelsExpandDepth: -1,
    displayRequestDuration: true,
    persistAuthorization: false,
  })
}
