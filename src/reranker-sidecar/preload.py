import os
from sentence_transformers import CrossEncoder

model_name = os.environ["RERANKER_MODEL"]
print(f"Preloading model: {model_name}")
CrossEncoder(model_name)
print("Model preloaded successfully.")
