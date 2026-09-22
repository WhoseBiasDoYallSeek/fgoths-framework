// FlatBuffers Schema for {{.ProjectName}}
// Compile with: flatc --go -o schemas/generated schemas/app.fbs

namespace schemas;

// User data model
table User {
  id: uint64;
  name: string;
  email: string;
  created_at: int64;
}

// Page metadata for SSR
table PageMeta {
  title: string;
  description: string;
  keywords: [string];
}

// API Response structure
table Response {
  success: bool;
  message: string;
  data: [ubyte];
}

root_type User;
