"""Administrative client for agent registration and platform management.

Most callers should use :func:`alancoin.connect` for spending.  Import
:class:`Alancoin` from this module only when you need to register agents,
manage services, or perform other admin operations::

    from alancoin.admin import Alancoin

    client = Alancoin("http://localhost:8080", api_key="ak_...")
    result = client.register(address="0x...", name="MyAgent")
"""

from .client import Alancoin

__all__ = ["Alancoin"]
