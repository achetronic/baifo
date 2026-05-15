# embeddings

In-process text embeddings, compiled into the `baifo` binary. No cgo, no
external model runtime, no API key, no network. The model
([`nomic-embed-text-v1.5`](https://huggingface.co/nomic-ai/nomic-embed-text-v1.5),
a ~137M-parameter BERT-style encoder) ships **inside** the binary via
`go:embed`, so `baifo` stays a single self-contained executable.

## Usage (library)

```go
eng, err := embeddings.New()           // loads the embedded model once
vec, err := eng.Embed("some text")     // []float32, length 768
unit, err := eng.EmbedNormalized(text) // L2-normalized, for cosine via dot
sim := embeddings.Cosine(a, b)         // cosine similarity
```

`New()` is lazy and cached: the weights are parsed once per process and the
returned `*Engine` is safe for concurrent use (the forward pass allocates
its own scratch per call; weights are read-only).

## How it works

The forward pass is a faithful pure-Go reimplementation of the
`NomicBertModel` architecture:

- **Tokenizer**: BERT WordPiece (uncased), vocab in `assets/vocab.txt`.
- **Encoder**: 12 post-norm transformer layers, hidden 768, 12 heads.
  - Rotary position embeddings (NeoX split convention, theta = 1000).
  - No learned position embeddings, no attention/MLP biases.
  - SwiGLU MLP: `fc2( silu(fc12 x) ⊙ fc11 x )`.
- **Pooling**: mean over all token positions (unnormalized).

Per-token matmuls are parallelized across `GOMAXPROCS` with stdlib
goroutines only.

### Quantization

Weights are stored **int8** (per-output-row symmetric scale) in
`assets/nomic-embed-text.weights`. This keeps the embedded blob at ~132MB
(vs ~547MB for raw f32) while preserving cosine similarity to within
~0.998 of the full-precision model - indistinguishable for retrieval.

### Weight blob format (`NMB1`)

```
magic   "NMB1"                           (4 bytes)
header  int32 x5: hidden, nLayers, nHeads, vocab, inter
        float32 x2: rope_theta, ln_eps
embeddings:
        qmat  word_embeddings   [vocab, hidden]
        f32   token_type[0]     [hidden]
        f32   emb_ln.weight, emb_ln.bias  [hidden] each
per layer (x nLayers):
        qmat  attn.Wqkv         [3*hidden, hidden]
        qmat  attn.out_proj     [hidden, hidden]
        qmat  mlp.fc11          [inter, hidden]
        qmat  mlp.fc12          [inter, hidden]
        qmat  mlp.fc2           [hidden, inter]
        f32   norm1.weight, norm1.bias, norm2.weight, norm2.bias [hidden]
```

A `qmat` is `rows*cols` int8 values followed by `rows` float32 scales;
element (r,c) = int8[r*cols+c] * scale[r].

## Regenerating the weights

The blob is produced offline from the upstream safetensors checkpoint:

```sh
# 1. fetch model.safetensors + vocab.txt from
#    huggingface.co/nomic-ai/nomic-embed-text-v1.5
# 2. quantize + pack:
python3 tools/convert.py model.safetensors assets/nomic-embed-text.weights
```

## Tests

`embeddings_test.go` checks the int8 Go engine against full-precision f32
reference vectors (`testdata_ref.json`, computed offline and themselves
validated against the `ollama nomic-embed-text` oracle's semantic
structure). The bar is cosine ≥ 0.99; in practice it lands at ~0.998. It
also asserts the property that actually matters for retrieval: related
sentences embed closer than unrelated ones.
