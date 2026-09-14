-- The vertex preset shipped a truncated base URL, and creating a provider
-- copies the preset's URL into its row. The Vertex adapter prefers any row URL
-- over the endpoint it builds from project and location, so those rows sent
-- every request to a path with neither. Only that exact URL is cleared: any
-- other value on a vertex row was set by an operator on purpose.
UPDATE providers
   SET base_url = ''
 WHERE kind = 'vertex'
   AND base_url = 'https://us-central1-aiplatform.googleapis.com/v1/projects';
