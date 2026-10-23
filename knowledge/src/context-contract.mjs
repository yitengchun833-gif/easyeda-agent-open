// Shared wire contract for Knowledge Service, MCP and the existing management UI.
// Used by the running service and MCP; context expansion is additive in v1.
const text = { type: 'string', minLength: 1, pattern: '\\S' };
const nullableText = { anyOf: [text, { type: 'null' }] };
const hash = { type: 'string', pattern: '^[a-f0-9]{64}$' };
const strings = { type: 'array', items: text, maxItems: 32 };
const object = properties => ({ type: 'object', properties, required: Object.keys(properties), additionalProperties: false });
const result = { enum: ['pass', 'fail', 'unknown'] };
const cursor = { type: 'string', minLength: 1, maxLength: 500 };

export const evidenceReferenceSchema = object({
  schemaVersion: { const: 1 },
  evidenceId: { type: 'string', pattern: '^ev-[a-f0-9]{64}$' },
  sourceId: text,
  sourceRevision: text,
  sourceHash: hash,
  snapshotHash: hash,
  selector: object({ chunkId: nullableText, anchor: nullableText })
});

export const knowledgeContextSchema = {
  ...object({
  schemaVersion: { const: 1 },
  kind: { enum: ['source', 'experience', 'artifact'] },
  title: text,
  sourceType: { enum: ['datasheet', 'company_rule', 'reference', 'lesson'] },
  domain: { enum: ['schematic', 'pcb', 'both', null] },
  evidenceRef: evidenceReferenceSchema,
  identity: object({ models: strings, manufacturer: nullableText, package: nullableText }),
  scope: object({ project: nullableText, module: nullableText, documentVersion: nullableText }),
  applicability: object({ conditions: nullableText, exclusions: nullableText }),
  verification: object({
    claimOrigin: { enum: ['proposer-claim', 'source-record', 'unknown'] },
    connectivity: result, layout: result, measurement: result,
    engineerReview: { enum: ['accepted', 'rejected', 'unknown'] },
    scope: nullableText, note: nullableText
  }),
  states: object({
    review: { enum: ['approved', 'pending', 'rejected', 'unknown'] },
    parse: { enum: ['success', 'partial', 'failed', 'unknown'] },
    index: { enum: ['keyword_ready', 'ready', 'failed', 'unknown'] }
  }),
  provenance: object({
    file: text, parser: nullableText,
    locations: { type: 'array', items: object({
      anchor: nullableText,
      pages: { type: 'array', items: { type: 'integer', minimum: 1 }, uniqueItems: true }
    }) }
  }),
  artifacts: strings,
  engineeringRefs: { type: 'array', maxItems: 32, items: object({
    kind: { enum: ['block', 'part', 'rule'] }, id: text, version: nullableText
  }) },
  content: {
    ...object({
      text: { type: 'string', maxLength: 30000 },
      readScope: { enum: ['excerpt', 'chunk', 'section', 'source'] },
      truncated: { type: 'boolean' },
      nextCursor: { anyOf: [cursor, { type: 'null' }] },
      contextCompleteness: { enum: ['complete', 'partial', 'unknown'] }
    }),
    properties: {
      text: { type: 'string', maxLength: 30000 },
      readScope: { enum: ['excerpt', 'chunk', 'section', 'source'] },
      truncated: { type: 'boolean' }, nextCursor: { anyOf: [cursor, { type: 'null' }] },
      contextCompleteness: { enum: ['complete', 'partial', 'unknown'] },
      contextExpansion: object({
        basis: {enum:['docling_structure','markdown_headings','same_page','neighbors','source','exact']},
        relationStatus: {enum:['confirmed','unconfirmed','not_applicable']},
        warnings: {type:'array',items:text}
      })
    },
    allOf: [
      {
        if: { properties: { readScope: { const: 'excerpt' } } },
        then: { properties: { nextCursor: { type: 'null' }, contextCompleteness: { enum: ['partial', 'unknown'] } } },
        else: {
          if: { properties: { truncated: { const: true } } },
          then: { properties: { nextCursor: cursor, contextCompleteness: { enum: ['partial', 'unknown'] } } },
          else: { properties: { nextCursor: { type: 'null' } } }
        }
      }
    ]
  }
  }),
  allOf: [
    { if: { properties: { kind: { const: 'experience' } } }, then: { properties: { sourceType: { const: 'lesson' } } } },
    { if: { properties: { sourceType: { const: 'lesson' } } }, then: { properties: { kind: { const: 'experience' } } } },
    { if: { properties: { kind: { const: 'artifact' } } }, then: { properties: { sourceType: { const: 'reference' } } } }
  ]
};

// Envelopes permit legacy aliases during migration; the shared context is strict.
export const knowledgeSearchOutputSchema = {
  ...object({
    schemaVersion: { const: 1 },
    items: { type: 'array', maxItems: 50, items: { ...object({
      context: { ...knowledgeContextSchema, properties: {
        ...knowledgeContextSchema.properties,
        states: { ...knowledgeContextSchema.properties.states, properties: {
          ...knowledgeContextSchema.properties.states.properties, review: { const: 'approved' }
        } },
        content: { ...knowledgeContextSchema.properties.content, properties: {
          ...knowledgeContextSchema.properties.content.properties, readScope: { const: 'excerpt' }
        } }
      } },
      score: { type: 'number', minimum: 0 }, matchReasons: strings
    }), additionalProperties: true } },
    retrieval: object({ mode: { enum: ['hybrid', 'keyword'] }, degradedReason: nullableText })
  }),
  additionalProperties: true
};

export const knowledgeGetOutputSchema = {
  ...object({
    schemaVersion: { const: 1 },
    context: { ...knowledgeContextSchema, properties: {
      ...knowledgeContextSchema.properties,
      content: { ...knowledgeContextSchema.properties.content, properties: {
        ...knowledgeContextSchema.properties.content.properties,
        readScope: { enum: ['chunk', 'section', 'source'] }
      } }
    } },
    readMode: { enum: ['pinned', 'current_unpinned'] }
  }),
  additionalProperties: true
};

export const knowledgeGetInputSchema = {
  type: 'object', additionalProperties: false,
  properties: {
    evidenceRef: evidenceReferenceSchema, id: text, anchor: text,
    includeVisuals: { type: 'boolean', description: 'Return frozen PNG images for the units in this read. Requires evidenceRef for image bytes.' },
    assetIds: { type: 'array', minItems: 1, maxItems: 3, uniqueItems: true, items: { type: 'string', minLength: 1, maxLength: 300 }, description: 'Optional registered image IDs (chunk IDs) in this read; implies includeVisuals.' },
    scope: { enum: ['chunk', 'section', 'source'] }, cursor,
    maxChars: { type: 'integer', minimum: 200, maximum: 30000 },
    limit: { type: 'integer', minimum: 1, maximum: 100 }
  },
  oneOf: [
    { required: ['evidenceRef'], not: { anyOf: [{ required: ['id'] }, { required: ['anchor'] }] } },
    { required: ['id'], not: { required: ['evidenceRef'] } }
  ],
  allOf: [
    {
      if: { required: ['evidenceRef', 'scope'], properties: { scope: { enum: ['chunk', 'section'] } } },
      then: { properties: { evidenceRef: { properties: { selector: { anyOf: [
        { properties: { chunkId: text } }, { properties: { anchor: text } }
      ] } } } } }
    }
  ]
};
