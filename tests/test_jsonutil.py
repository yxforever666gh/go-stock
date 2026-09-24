import math

import pytest

from stock_god.jsonutil import dumps


@pytest.mark.parametrize(
    "value,expected",
    [
        (1.0, "1"),
        (-0.0, "-0"),
        (1e-6, "0.000001"),
        (1e-7, "1e-7"),
        (1e20, "100000000000000000000"),
        (1e21, "1e+21"),
        (10.25, "10.25"),
        ({"z": "<股票>&", "a": [None, True, 0.0]}, '{"a":[null,true,0],"z":"\\u003c股票\\u003e\\u0026"}'),
    ],
)
def test_go_encoding_contract(value, expected):
    assert dumps(value) == expected


def test_struct_order_and_nonfinite_rejection():
    assert dumps({"z": 1, "a": 2}, sort_keys=False) == '{"z":1,"a":2}'
    with pytest.raises(ValueError):
        dumps(math.nan)
