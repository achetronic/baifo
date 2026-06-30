#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
# SPDX-License-Identifier: Apache-2.0

# Converts nomic-embed-text-v1.5 safetensors (f32) into baifo's compact
# int8 weight blob. Per-row symmetric quantization (cos>0.998 vs f32).
import struct, json, sys
import numpy as np

SRC = sys.argv[1] if len(sys.argv) > 1 else 'model.safetensors'
OUT = sys.argv[2] if len(sys.argv) > 2 else 'nomic-embed-text.weights'

with open(SRC, 'rb') as f:
    n = struct.unpack('<Q', f.read(8))[0]
    hdr = json.loads(f.read(n))
    blob = f.read()

def T(name):
    h = hdr[name]
    assert h['dtype'] == 'F32', h['dtype']
    s, e = h['data_offsets']
    return np.frombuffer(blob[s:e], dtype=np.float32).reshape(h['shape']).copy()

out = open(OUT, 'wb')

def w_f32(arr):
    out.write(np.asarray(arr, dtype='<f4').tobytes())

def w_q8(W):
    # per-row symmetric int8
    W = np.asarray(W, dtype=np.float32)
    scale = np.abs(W).max(axis=1, keepdims=True) / 127.0
    scale[scale == 0] = 1.0
    q = np.round(W / scale).astype(np.int8)
    out.write(q.tobytes())                       # rows*cols int8
    out.write(scale.reshape(-1).astype('<f4').tobytes())  # rows f32

HIDDEN=768; NLAYERS=12; NHEADS=12; VOCAB=30528; INTER=3072
# header
out.write(b'NMB1')
out.write(struct.pack('<iiiii', HIDDEN, NLAYERS, NHEADS, VOCAB, INTER))
out.write(struct.pack('<ff', 1000.0, 1e-12))  # rope_theta, ln_eps

# embeddings
w_q8(T('embeddings.word_embeddings.weight'))          # [30528,768]
w_f32(T('embeddings.token_type_embeddings.weight')[0])# [768]
w_f32(T('emb_ln.weight')); w_f32(T('emb_ln.bias'))

for L in range(NLAYERS):
    p=f'encoder.layers.{L}.'
    w_q8(T(p+'attn.Wqkv.weight'))      # [2304,768]
    w_q8(T(p+'attn.out_proj.weight'))  # [768,768]
    w_q8(T(p+'mlp.fc11.weight'))       # [3072,768]
    w_q8(T(p+'mlp.fc12.weight'))       # [3072,768]
    w_q8(T(p+'mlp.fc2.weight'))        # [768,3072]
    w_f32(T(p+'norm1.weight')); w_f32(T(p+'norm1.bias'))
    w_f32(T(p+'norm2.weight')); w_f32(T(p+'norm2.bias'))

out.close()
import os
print('wrote', OUT, os.path.getsize(OUT), 'bytes')
