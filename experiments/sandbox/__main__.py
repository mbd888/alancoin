"""Entry point: python -m experiments.sandbox"""

import uvicorn
from .app import app

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8090)
else:
    # Also run when invoked as: python -m experiments.sandbox
    uvicorn.run(app, host="0.0.0.0", port=8090)
