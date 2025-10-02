{
  applications.demo = {
    namespace = "demo";

    # Automatically generate a namespace resource with the
    # above set namespace
    createNamespace = true;

    resources = let
      labels = {
        "app.kubernetes.io/name" = "nginx";
      };
    in {
      # Define a deployment for running an nginx server
      deployments.nginx.spec = {
        selector.matchLabels = labels;
        template = {
          metadata.labels = labels;
          spec = {
            securityContext.fsGroup = 1000;
            containers.nginx = {
              image = "nginx:1.25.1";
              imagePullPolicy = "IfNotPresent";
              volumeMounts = {
                "/etc/nginx".name = "config";
                "/var/lib/html".name = "static";
              };
            };
            volumes = {
              config.configMap.name = "nginx-config";
              static.configMap.name = "nginx-static";
            };
          };
        };
      };

      # Define config maps with config for nginx
      configMaps = {
        nginx-config.data."nginx.conf" = ''
          user nginx nginx;
          error_log /dev/stdout info;
          pid /dev/null;
          events {}
          http {
            access_log /dev/stdout;
            server {
              listen 80;
              index index.html;
              location / {
                root /var/lib/html;
              }
            }
          }
        '';

        nginx-static.data."index.html" = ''
          <html><body><h1>Hello from NGINX</h1></body></html>
        '';
      };

      # Define service for nginx
      services.nginx.spec = {
        selector = labels;
        ports.http.port = 80;
      };
    };
  };
}
